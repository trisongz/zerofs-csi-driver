package csi

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const ZeroFSRunDir = "/run/zerofs-csi"

type nodeService struct {
	csi.UnimplementedNodeServer
	name    string
	nodeID  string
	logger  *logrus.Logger
	mounter *zerofsMounter

	nbdMu sync.Mutex
}

func (ns *nodeService) reconcileOrphanedNBDDevices(ctx context.Context) {
	// Keep this best-effort and non-blocking: we don't want startup reconciliation to prevent the node plugin from serving.
	ns.logger.Infof("Running startup NBD reconciler (best-effort)")

	devices, err := listNBDDevices()
	if err != nil {
		ns.logger.Warnf("Failed to list /dev/nbd* devices: %v", err)
		return
	}

	detached := 0
	skippedMounted := 0
	for _, dev := range devices {
		select {
		case <-ctx.Done():
			return
		default:
		}

		connected, err := isNBDConnected(dev)
		if err != nil || !connected {
			continue
		}

		// If mounted anywhere, leave it alone (it's in use by a workload).
		cmd := exec.Command("findmnt", "-n", "-S", dev)
		out, err := cmd.CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			skippedMounted++
			continue
		}

		ns.logger.Warnf("Detaching orphaned NBD device %s (connected but not mounted)", dev)
		detachNBDDeviceBestEffort(ns.logger, dev)
		detached++
	}

	ns.logger.Infof("Startup NBD reconciler complete: detached=%d skipped_mounted=%d", detached, skippedMounted)
}

// NodeStageVolume implements NodeServer
func (ns *nodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	ns.logger.Infof("NodeStageVolume called: %s", req.VolumeId)

	// Validate request
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID cannot be empty")
	}

	if req.StagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path cannot be empty")
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume implements NodeServer
func (ns *nodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	ns.logger.Infof("NodeUnstageVolume called: %s", req.VolumeId)

	// Validate request
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID cannot be empty")
	}

	if req.StagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path cannot be empty")
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume implements NodeServer
func (ns *nodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	ns.logger.Infof("NodePublishVolume called: %s", req.VolumeId)
	opTimer := startStage("node_publish", "total")

	// Validate request
	if req.VolumeId == "" {
		recordStageError("node_publish", "validate")
		return nil, status.Error(codes.InvalidArgument, "volume ID cannot be empty")
	}

	if req.TargetPath == "" {
		recordStageError("node_publish", "validate")
		return nil, status.Error(codes.InvalidArgument, "target path cannot be empty")
	}

	if req.VolumeCapability == nil {
		recordStageError("node_publish", "validate")
		return nil, status.Error(codes.InvalidArgument, "volume capability cannot be empty")
	}

	volumeID := req.VolumeId
	nodeName := ns.nodeID

	filesystem, exists := req.VolumeContext["filesystem"]
	if !exists {
		ns.logger.Errorf("Failed to determine filesystem for volume, ensure filesystem is specified")
		recordStageError("node_publish", "validate")
		return nil, status.Error(codes.InvalidArgument, "failed to determine filesystem for volume, ensure filesystem is specified")
	}

	// Idempotency: if the target is already mounted from an NBD device, return success.
	if src, mounted := findMountSource(req.TargetPath); mounted {
		if strings.HasPrefix(src, "/dev/nbd") {
			ns.logger.Infof("Target path %s is already mounted from %s for volume %s; returning success", req.TargetPath, src, volumeID)
			opTimer.observe()
			return &csi.NodePublishVolumeResponse{}, nil
		}
		// Recover from a partially-failed attempt (e.g. lingering 9p mount).
		ns.logger.Warnf("Target path %s is already mounted from %s for volume %s; attempting to unmount and retry", req.TargetPath, src, volumeID)
		unmountBestEffort(ns.logger, req.TargetPath)
	}

	// Best-effort cleanup of stale NBD reservations from previous failed attempts.
	ns.nbdMu.Lock()
	if staleDev, ok := readVolumeNBDState(volumeID); ok {
		if connected, err := isNBDConnected(staleDev); err == nil && connected {
			ns.logger.Warnf("Found stale NBD state for volume %s pointing to %s; detaching before retry", volumeID, staleDev)
			detachNBDDeviceBestEffort(ns.logger, staleDev)
		}
		clearVolumeNBDState(volumeID, staleDev)
	}
	ns.nbdMu.Unlock()

	ns.logger.Infof("Mounting volume %s to %s on node %s", volumeID, req.TargetPath, nodeName)

	published := false
	defer func() {
		if published {
			return
		}

		// Cleanup for failed publishes to avoid leaking NBD devices and per-volume pods.
		unmountBestEffort(ns.logger, req.TargetPath)

		if dev, ok := readVolumeNBDState(volumeID); ok {
			detachNBDDeviceBestEffort(ns.logger, dev)
			clearVolumeNBDState(volumeID, dev)
		}

		if err := ns.mounter.RemovePod(ctx, volumeID); err != nil {
			ns.logger.Warnf("Failed to remove pod during failed publish cleanup for volume %s: %v", volumeID, err)
		}
	}()

	// Create the target directory if it doesn't exist
	if err := os.MkdirAll(req.TargetPath, 0755); err != nil {
		ns.logger.Errorf("Failed to create target directory %s: %v", req.TargetPath, err)
		recordStageError("node_publish", "mkdir")
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create target directory: %v", err))
	}

	configMapName, exists := req.VolumeContext["configMapName"]
	if !exists {
		ns.logger.Errorf("Failed to determine configMapName for volume, ensure configMapName is specified")
		recordStageError("node_publish", "validate")
		return nil, status.Error(codes.InvalidArgument, "failed to determine configMapName for volume, ensure configMapName is specified")
	}

	// Create pod for the zerofs server
	stage := startStage("node_publish", "create_pod")
	if err := ns.mounter.CreatePod(ctx, volumeID, nodeName, configMapName); err != nil {
		ns.logger.Errorf("Failed to create pod for volume %s: %v", volumeID, err)
		recordStageError("node_publish", "create_pod")
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create pod: %v", err))
	}
	stage.observe()

	socketPath := filepath.Join(ZeroFSBaseDir, fmt.Sprintf("zerofs-%s.9p.sock", volumeID))
	nbdSocketPath := filepath.Join(ZeroFSBaseDir, fmt.Sprintf("zerofs-%s.nbd.sock", volumeID))

	// Add a retry loop to check for socket file existence before attempting mount
	// This handles race conditions where the service might not have fully initialized
	maxRetries := 6
	retryDelay := 10 * time.Second

	ns.logger.Infof("Checking if socket file exists before mounting: %s", socketPath)

	for attempt := 0; attempt < maxRetries; attempt++ {
		if _, err := os.Stat(socketPath); err == nil {
			// Socket file exists, proceed with mount
			break
		} else if os.IsNotExist(err) {
			ns.logger.Warnf("Socket file does not exist (attempt %d/%d): %s", attempt+1, maxRetries, socketPath)
			if attempt < maxRetries-1 { // Don't sleep on the last attempt
				time.Sleep(retryDelay)
				continue
			}
		} else {
			// Some other error occurred when checking file
			ns.logger.Errorf("Error checking socket file: %v", err)
			recordStageError("node_publish", "wait_socket")
			return nil, status.Error(codes.Internal, fmt.Sprintf("error checking socket file: %v", err))
		}

		// If we reach here, it means we've exhausted retries and socket still doesn't exist
		ns.logger.Errorf("Socket file still does not exist after %d attempts: %s", maxRetries, socketPath)

		recordStageError("node_publish", "wait_socket")
		return nil, status.Error(codes.Internal, fmt.Sprintf("socket file does not exist after %d attempts: %s", maxRetries, socketPath))
	}

	stage = startStage("node_publish", "mount_9p")
	cmd := exec.Command("mount", "-t", "9p", "-o", "trans=unix,version=9p2000.L", socketPath, req.TargetPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If it is already mounted (from a retry/partial attempt), continue rather than leaking NBD devices.
		if src, mounted := findMountSource(req.TargetPath); mounted {
			ns.logger.Warnf("9p mount returned error, but target %s is mounted from %s; continuing. err=%v output=%s", req.TargetPath, src, err, strings.TrimSpace(string(output)))
		} else {
			ns.logger.Errorf("Failed to mount volume %s to %s: %v, output: %s", volumeID, req.TargetPath, err, string(output))
			recordStageError("node_publish", "mount_9p")
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to mount volume: %v", err))
		}
	} else {
		ns.logger.Infof("Volume %s mounted successfully using 9p protocol", volumeID)
	}
	stage.observe()

	// we need to make an nbd volume with the filesystem provided
	ns.logger.Infof("Creating NBD volume with filesystem: %s", filesystem)

	// if the disk file doesn't exist, create it
	diskFilePath := filepath.Join(req.TargetPath, ".nbd", "disk")
	if _, err := os.Stat(diskFilePath); os.IsNotExist(err) {
		ns.logger.Infof("Creating disk file at %s", diskFilePath)

		// Get volume capacity from context
		capacityBytes, exists := req.VolumeContext["capacity_bytes"]

		if !exists {
			ns.logger.Errorf("No capacity information found in volume context")
			return nil, status.Error(codes.InvalidArgument, "no capacity information provided for volume")
		}

		ns.logger.Infof("Creating disk file with size: %s", capacityBytes)
		stage = startStage("node_publish", "create_disk")
		cmd := exec.Command("truncate", "-s", capacityBytes, diskFilePath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			ns.logger.Errorf("Failed to create disk file %s: %v, output: %s", diskFilePath, err, string(output))
			recordStageError("node_publish", "create_disk")
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create disk file: %v", err))
		}
		stage.observe()

		ns.logger.Infof("Successfully created disk file at %s with size %s", diskFilePath, capacityBytes)
	} else if err != nil {
		ns.logger.Errorf("Error checking disk file %s: %v", diskFilePath, err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("error checking disk file: %v", err))
	} else {
		ns.logger.Infof("Disk file %s already exists", diskFilePath)
	}

	// we no longer need the 9p filesystem, as we have a disk we can mount via nbd instead
	stage = startStage("node_publish", "umount_9p")
	cmd = exec.Command("umount", req.TargetPath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		ns.logger.Errorf("Failed to unmount 9p volume %s from %s: %v, output: %s", req.VolumeId, req.TargetPath, err, string(output))
		recordStageError("node_publish", "umount_9p")
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to unmount 9p volume: %v", err))
	}
	stage.observe()

	// Check if we already have a tracking file with an NBD device path
	var nbdDevicePath string
	ns.nbdMu.Lock()
	nbdDevicePath, err = reserveNBDDevice(ns.logger, volumeID)
	ns.nbdMu.Unlock()
	if err != nil {
		ns.logger.Errorf("Failed to reserve an NBD device for volume %s: %v", volumeID, err)
		recordStageError("node_publish", "reserve_nbd")
		return nil, status.Error(codes.ResourceExhausted, fmt.Sprintf("failed to reserve NBD device: %v", err))
	}

	// Connect to the NBD server using an explicit device to avoid parsing output and leaking connections.
	stage = startStage("node_publish", "connect_nbd")
	cmd = exec.Command("nbd-client", "-unix", nbdSocketPath, "-N", "disk", "-persist", "-timeout", "0", "-connections", "4", nbdDevicePath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		ns.logger.Errorf("Failed to connect to NBD device %s: %v output: %s", nbdDevicePath, err, string(output))
		// Clear reservation so retries can pick another device.
		ns.nbdMu.Lock()
		clearVolumeNBDState(volumeID, nbdDevicePath)
		ns.nbdMu.Unlock()
		recordStageError("node_publish", "connect_nbd")
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to connect to NBD device: %v output: %s", err, string(output)))
	}
	stage.observe()

	// Check if the device already has a filesystem
	ns.logger.Infof("Checking if NBD device %s already has a filesystem", nbdDevicePath)
	cmd = exec.Command("blkid", "-o", "value", "-s", "TYPE", nbdDevicePath)
	output, _ = cmd.CombinedOutput()
	// Check if the output is empty (no filesystem found)
	fsType := strings.TrimSpace(string(output))
	if fsType != "" {
		ns.logger.Infof("NBD device %s already has filesystem of type: %s", nbdDevicePath, fsType)
		// Device already has a filesystem, skip formatting
		ns.logger.Infof("Skipping mkfs as filesystem already exists on %s", nbdDevicePath)
	} else {
		// No filesystem found, proceed with formatting
		ns.logger.Infof("Formatting NBD device %s with filesystem: %s", nbdDevicePath, filesystem)

		mkfsCmd, mkfsArgs := mkfsCommand(filesystem, nbdDevicePath)
		stage = startStage("node_publish", "mkfs")
		cmd = exec.Command(mkfsCmd, mkfsArgs...)
		output, err = cmd.CombinedOutput()
		if err != nil {
			// Cleanup NBD device on failure
			ns.logger.Errorf("Failed to format NBD device %s: %v, output: %s. Disconnecting NBD...", nbdDevicePath, err, string(output))
			recordStageError("node_publish", "mkfs")
			cleanupCmd := exec.Command("nbd-client", "-d", nbdDevicePath)
			if cleanupErr := cleanupCmd.Run(); cleanupErr != nil {
				ns.logger.Warnf("Failed to disconnect NBD device %s during cleanup: %v", nbdDevicePath, cleanupErr)
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to format NBD device: %v, output: %s", err, strings.TrimSpace(string(output))))
		}
		stage.observe()
		ns.logger.Infof("Successfully formatted NBD device %s with filesystem %s", nbdDevicePath, filesystem)

	}

	// Mount the NBD device to the target path
	ns.logger.Infof("Mounting NBD device %s to %s", nbdDevicePath, req.TargetPath)

	mountOptions := req.GetVolumeCapability().GetMount().GetMountFlags()

	// Create mount command for NBD device with specified filesystem and options
	var mountArgs []string
	mountArgs = append(mountArgs, "-t", filesystem)

	// Add mount options if they exist
	if len(mountOptions) > 0 {
		// Join the options into a single comma-separated string
		optionsString := strings.Join(mountOptions, ",")
		mountArgs = append(mountArgs, "-o", optionsString)
	}

	mountArgs = append(mountArgs, nbdDevicePath, req.TargetPath)

	cmd = exec.Command("mount", mountArgs...)
	stage = startStage("node_publish", "mount_fs")
	output, err = cmd.CombinedOutput()
	if err != nil {
		// Cleanup NBD device on failure
		ns.logger.Errorf("Failed to mount NBD device %s to %s: %v, output: %s. Disconnecting NBD...", nbdDevicePath, req.TargetPath, err, string(output))
		recordStageError("node_publish", "mount_fs")
		cleanupCmd := exec.Command("nbd-client", "-d", nbdDevicePath)
		if cleanupErr := cleanupCmd.Run(); cleanupErr != nil {
			ns.logger.Warnf("Failed to disconnect NBD device %s during cleanup: %v", nbdDevicePath, cleanupErr)
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to mount NBD device: %v", err))
	}
	stage.observe()

	ns.logger.Infof("Volume %s mounted successfully using NBD device %s", volumeID, nbdDevicePath)

	published = true

	opTimer.observe()
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume implements NodeServer
func (ns *nodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	ns.logger.Infof("NodeUnpublishVolume called: %s", req.VolumeId)
	opTimer := startStage("node_unpublish", "total")

	// Validate request
	if req.VolumeId == "" {
		recordStageError("node_unpublish", "validate")
		return nil, status.Error(codes.InvalidArgument, "volume ID cannot be empty")
	}

	if req.TargetPath == "" {
		recordStageError("node_unpublish", "validate")
		return nil, status.Error(codes.InvalidArgument, "target path cannot be empty")
	}

	// Determine which nbd block device this volume is using so we can detach it after unmounting
	var nbdDevice string

	// First try to find it via findmnt
	if src, mounted := findMountSource(req.TargetPath); mounted {
		nbdDevice = src
	} else {
		ns.logger.Warnf("Failed to find volume mount context for %s from %s", req.VolumeId, req.TargetPath)
	}

	// Unmount the filesystem (always attempt unmount, even if findmnt failed)
	ns.logger.Infof("Unmounting volume %s from %s", req.VolumeId, req.TargetPath)
	cmd := exec.Command("umount", req.TargetPath)
	stage := startStage("node_unpublish", "umount_fs")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// verify if it is actually unmounted
		if strings.Contains(string(output), "not mounted") {
			ns.logger.Infof("Volume %s already unmounted from %s", req.VolumeId, req.TargetPath)
		} else {
			ns.logger.Warnf("Failed to unmount volume %s from %s: %v, output: %s", req.VolumeId, req.TargetPath, err, string(output))
			recordStageError("node_unpublish", "umount_fs")
		}
	}
	stage.observe()

	// Check /run/zerofs-csi/ for the NBD device if findmnt didn't return it
	nbdDeviceFilePath := volumeNBDStatePath(req.VolumeId)
	if nbdDevice == "" {
		if nbdDeviceBytes, err := os.ReadFile(nbdDeviceFilePath); err == nil {
			nbdDevice = strings.TrimSpace(string(nbdDeviceBytes))
			ns.logger.Infof("Recovered NBD device %s from state file for volume %s", nbdDevice, req.VolumeId)
		} else {
			ns.logger.Warnf("Could not determine NBD device for volume %s (findmnt failed and state file missing/unreadable)", req.VolumeId)
		}
	}

	// Remove the nbd-client device
	if nbdDevice != "" {
		ns.logger.Infof("Detaching volume %s from %s", req.VolumeId, nbdDevice)
		cmd = exec.Command("nbd-client", "-d", nbdDevice)
		stage = startStage("node_unpublish", "detach_nbd")
		_, err = cmd.CombinedOutput()
		if err != nil {
			ns.logger.Warnf("Failed to detach nbd for volume %s: %v", req.VolumeId, err)
			recordStageError("node_unpublish", "detach_nbd")
		}
		stage.observe()
	} else {
		ns.logger.Warnf("Skipping NBD detach for volume %s as device could not be determined", req.VolumeId)
	}

	// Remove pod for the zerofs server
	stage = startStage("node_unpublish", "remove_pod")
	if err := ns.mounter.RemovePod(ctx, req.VolumeId); err != nil {
		ns.logger.Warnf("Failed to remove pod for volume %s: %v", req.VolumeId, err)
		recordStageError("node_unpublish", "remove_pod")
		// Continue with cleanup even if pod removal fails
	}
	stage.observe()

	// Wait for pod deletion to complete before returning
	podName := fmt.Sprintf("zerofs-volume-%s", req.VolumeId)
	ns.logger.Infof("Waiting for pod %s to be fully deleted", podName)
	if err := ns.mounter.waitForPodDeletion(podName, ns.mounter.namespace, 30*time.Second); err != nil {
		ns.logger.Warnf("Warning: failed to wait for pod deletion: %v", err)
		// Continue with response even if we couldn't verify deletion
	}

	// Remove the tracking file from /run/zerofs-csi/
	ns.nbdMu.Lock()
	clearVolumeNBDState(req.VolumeId, nbdDevice)
	ns.nbdMu.Unlock()

	ns.logger.Infof("Volume %s unpublished successfully", req.VolumeId)
	opTimer.observe()
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats implements NodeServer
func (ns *nodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	ns.logger.Infof("NodeGetVolumeStats called: %s", req.VolumeId)

	// Validate request
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID cannot be empty")
	}

	if req.VolumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volume path cannot be empty")
	}

	// Use df command to get volume stats
	cmd := exec.Command("df", "-B1", req.VolumePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		ns.logger.Errorf("Failed to execute df command for %s: %v, output: %s", req.VolumePath, err, string(output))
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get volume stats: %v", err))
	}

	// Parse the output
	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		ns.logger.Errorf("Unexpected df output format for %s: %s", req.VolumePath, string(output))
		return nil, status.Error(codes.Internal, "unexpected df output format")
	}

	// Extract the stats line (second line)
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		ns.logger.Errorf("Unexpected number of fields in df output for %s: %s", req.VolumePath, string(output))
		return nil, status.Error(codes.Internal, "unexpected number of fields in df output")
	}

	totalBytes, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		ns.logger.Errorf("Failed to parse total bytes from df output for %s: %v", req.VolumePath, err)
		return nil, status.Error(codes.Internal, "failed to parse volume stats")
	}

	usedBytes, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		ns.logger.Errorf("Failed to parse used bytes from df output for %s: %v", req.VolumePath, err)
		return nil, status.Error(codes.Internal, "failed to parse volume stats")
	}

	availableBytes, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		ns.logger.Errorf("Failed to parse available bytes from df output for %s: %v", req.VolumePath, err)
		return nil, status.Error(codes.Internal, "failed to parse volume stats")
	}

	ns.logger.Infof(
		"Volume %s stats retrieved. Total: %d bytes, Used: %d bytes, Available: %d bytes",
		req.VolumeId,
		totalBytes,
		usedBytes,
		availableBytes,
	)

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Total:     totalBytes,
				Used:      usedBytes,
				Available: availableBytes,
			},
		},
	}, nil
}

// NodeGetCapabilities implements NodeServer
func (ns *nodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	ns.logger.Infof("NodeGetCapabilities called")

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
					},
				},
			},
		},
	}, nil
}

func mkfsCommand(filesystem, devicePath string) (string, []string) {
	fs := strings.ToLower(strings.TrimSpace(filesystem))
	switch fs {
	case "btrfs":
		return "mkfs.btrfs", []string{"-f", devicePath}
	case "xfs":
		return "mkfs.xfs", []string{"-f", devicePath}
	case "ext2", "ext3", "ext4":
		// e2fsprogs uses -F to force on block devices
		return "mkfs." + fs, []string{"-F", devicePath}
	default:
		return "mkfs", []string{"-t", fs, devicePath}
	}
}

// NodeGetInfo implements NodeServer
func (ns *nodeService) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	ns.logger.Infof("NodeGetInfo called for node: %s", ns.nodeID)

	return &csi.NodeGetInfoResponse{
		NodeId:            ns.nodeID,
		MaxVolumesPerNode: 0,
	}, nil
}
