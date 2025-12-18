package csi

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	nbdByDeviceDir = ZeroFSRunDir + "/by-device"
)

var nbdDevicePathRe = regexp.MustCompile(`^/dev/nbd\d+$`)

func ensureRunDirs() error {
	if err := os.MkdirAll(ZeroFSRunDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(nbdByDeviceDir, 0755); err != nil {
		return err
	}
	return nil
}

func volumeNBDStatePath(volumeID string) string {
	// Backwards compatible with the original layout (/run/zerofs-csi/<volumeID>).
	return filepath.Join(ZeroFSRunDir, volumeID)
}

func deviceReservationPath(nbdDevicePath string) string {
	return filepath.Join(nbdByDeviceDir, filepath.Base(strings.TrimSpace(nbdDevicePath)))
}

func listNBDDevices() ([]string, error) {
	matches, err := filepath.Glob("/dev/nbd*")
	if err != nil {
		return nil, err
	}

	devices := make([]string, 0, len(matches))
	for _, m := range matches {
		if nbdDevicePathRe.MatchString(m) {
			devices = append(devices, m)
		}
	}

	sort.Slice(devices, func(i, j int) bool {
		ai := strings.TrimPrefix(filepath.Base(devices[i]), "nbd")
		aj := strings.TrimPrefix(filepath.Base(devices[j]), "nbd")
		ni, _ := strconv.Atoi(ai)
		nj, _ := strconv.Atoi(aj)
		return ni < nj
	})

	return devices, nil
}

func isNBDConnected(nbdDevicePath string) (bool, error) {
	dev := filepath.Base(strings.TrimSpace(nbdDevicePath)) // nbd0
	pidPath := filepath.Join("/sys/block", dev, "pid")
	b, err := os.ReadFile(pidPath)
	if err != nil {
		// If we cannot determine connection state, err on the side of "unknown".
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", pidPath, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", pidPath, err)
	}
	return pid > 0, nil
}

func findMountSource(targetPath string) (string, bool) {
	cmd := exec.Command("findmnt", "-n", "-o", "SOURCE", "--target", targetPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", false
	}
	src := strings.TrimSpace(string(out))
	return src, src != ""
}

func unmountBestEffort(logger *logrus.Logger, targetPath string) {
	if targetPath == "" {
		return
	}
	if _, mounted := findMountSource(targetPath); !mounted {
		return
	}
	cmd := exec.Command("umount", targetPath)
	out, err := cmd.CombinedOutput()
	if err != nil && logger != nil {
		logger.Warnf("umount %s failed: %v, output: %s", targetPath, err, strings.TrimSpace(string(out)))
	}
}

func detachNBDDeviceBestEffort(logger *logrus.Logger, nbdDevicePath string) {
	nbdDevicePath = strings.TrimSpace(nbdDevicePath)
	if nbdDevicePath == "" {
		return
	}

	// Retry a few times; detach can transiently fail if the kernel is still releasing the mount.
	for attempt := 1; attempt <= 5; attempt++ {
		cmd := exec.Command("nbd-client", "-d", nbdDevicePath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return
		}
		if logger != nil {
			logger.Warnf("nbd detach failed (attempt %d/5) for %s: %v, output: %s", attempt, nbdDevicePath, err, strings.TrimSpace(string(out)))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func readVolumeNBDState(volumeID string) (string, bool) {
	b, err := os.ReadFile(volumeNBDStatePath(volumeID))
	if err != nil {
		return "", false
	}
	dev := strings.TrimSpace(string(b))
	if dev == "" {
		return "", false
	}
	return dev, true
}

func writeVolumeNBDState(volumeID, nbdDevicePath string) error {
	if err := ensureRunDirs(); err != nil {
		return err
	}
	return os.WriteFile(volumeNBDStatePath(volumeID), []byte(strings.TrimSpace(nbdDevicePath)), 0644)
}

func clearVolumeNBDState(volumeID string, nbdDevicePath string) {
	_ = os.Remove(volumeNBDStatePath(volumeID))
	if nbdDevicePath != "" {
		_ = os.Remove(deviceReservationPath(nbdDevicePath))
	}
}

// reserveNBDDevice picks a free /dev/nbdX and creates an atomic reservation for it.
// Callers must serialize calls (e.g. with a mutex) to avoid races while scanning.
func reserveNBDDevice(logger *logrus.Logger, volumeID string) (string, error) {
	if err := ensureRunDirs(); err != nil {
		return "", err
	}

	// If we already have a state file, try to reuse it (idempotent retries).
	if existing, ok := readVolumeNBDState(volumeID); ok {
		if _, err := os.Stat(existing); err == nil {
			// Best-effort ensure the by-device reservation exists too.
			resPath := deviceReservationPath(existing)
			if _, err := os.Stat(resPath); os.IsNotExist(err) {
				if f, err := os.OpenFile(resPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644); err == nil {
					_, _ = f.WriteString(volumeID)
					_ = f.Close()
				}
			}
			return existing, nil
		}
		// Stale state file pointing at a missing device.
		clearVolumeNBDState(volumeID, existing)
	}

	devs, err := listNBDDevices()
	if err != nil {
		return "", err
	}
	if len(devs) == 0 {
		return "", fmt.Errorf("no /dev/nbd* devices found (is the nbd module loaded and /dev mounted?)")
	}

	for _, dev := range devs {
		connected, err := isNBDConnected(dev)
		if err != nil {
			if logger != nil {
				logger.Warnf("Failed to determine NBD connection state for %s: %v", dev, err)
			}
			continue
		}
		if connected {
			continue
		}

		// Garbage-collect stale reservations: if the reservation exists but the volume state file doesn't,
		// it's safe to remove (a previous attempt crashed before connecting).
		resPath := deviceReservationPath(dev)
		if st, err := os.Stat(resPath); err == nil {
			// If the corresponding volume file is missing, clear this reservation.
			vol := strings.TrimSpace(string(mustReadFile(resPath)))
			if vol == "" {
				_ = os.Remove(resPath)
			} else if _, err := os.Stat(volumeNBDStatePath(vol)); os.IsNotExist(err) {
				_ = os.Remove(resPath)
			} else if time.Since(st.ModTime()) > 2*time.Hour {
				// Conservative fallback: clear very old reservations.
				_ = os.Remove(resPath)
			}
		}

		// Reserve with O_EXCL for safety.
		f, err := os.OpenFile(resPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			continue
		}
		_, _ = f.WriteString(volumeID)
		_ = f.Close()

		// Persist by-volume state immediately so we can clean up even if later steps fail.
		if err := writeVolumeNBDState(volumeID, dev); err != nil {
			_ = os.Remove(resPath)
			return "", err
		}

		return dev, nil
	}

	return "", fmt.Errorf("no free /dev/nbd* devices available")
}

func mustReadFile(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

