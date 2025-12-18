package csi

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (cs *controllerService) deleteVolumeDataIfEnabled(ctx context.Context, volumeID string) error {
	if cs.mounter == nil || cs.mounter.client == nil {
		return nil
	}

	pv, _ := cs.findPersistentVolume(ctx, volumeID)
	if pv == nil {
		// If the PV is already gone, we cannot resolve StorageClass/config. Treat as best-effort no-op.
		return nil
	}

	scName := strings.TrimSpace(pv.Spec.StorageClassName)
	if scName == "" && pv.Spec.ClaimRef != nil {
		var pvc corev1.PersistentVolumeClaim
		if err := cs.mounter.client.Get(ctx, types.NamespacedName{Namespace: pv.Spec.ClaimRef.Namespace, Name: pv.Spec.ClaimRef.Name}, &pvc); err == nil {
			if pvc.Spec.StorageClassName != nil {
				scName = strings.TrimSpace(*pvc.Spec.StorageClassName)
			}
		}
	}
	if scName == "" {
		return nil
	}

	var sc storagev1.StorageClass
	if err := cs.mounter.client.Get(ctx, types.NamespacedName{Name: scName}, &sc); err != nil {
		return nil
	}

	if !strings.EqualFold(strings.TrimSpace(sc.Parameters["deleteDataOnPVCDelete"]), "true") {
		return nil
	}

	configMapName := strings.TrimSpace(sc.Parameters["configMapName"])
	if configMapName == "" {
		return fmt.Errorf("deleteDataOnPVCDelete=true but StorageClass %q is missing parameters.configMapName", scName)
	}

	var cfgMap corev1.ConfigMap
	if err := cs.mounter.client.Get(ctx, types.NamespacedName{Namespace: cs.mounter.namespace, Name: configMapName}, &cfgMap); err != nil {
		return fmt.Errorf("failed to read configmap %s/%s: %w", cs.mounter.namespace, configMapName, err)
	}

	loc, err := parseS3Location(cfgMap.Data["storageURL"])
	if err != nil {
		return fmt.Errorf("failed to parse storageURL for %s/%s: %w", cs.mounter.namespace, configMapName, err)
	}
	loc.Prefix = joinS3Prefix(loc.Prefix, volumeID)

	credsSecretName := strings.TrimSpace(cfgMap.Data["awsCredentialsSecretName"])
	if credsSecretName == "" {
		return fmt.Errorf("configmap %s/%s missing awsCredentialsSecretName", cs.mounter.namespace, configMapName)
	}

	var creds corev1.Secret
	if err := cs.mounter.client.Get(ctx, types.NamespacedName{Namespace: cs.mounter.namespace, Name: credsSecretName}, &creds); err != nil {
		return fmt.Errorf("failed to read credentials secret %s/%s: %w", cs.mounter.namespace, credsSecretName, err)
	}

	accessKey := strings.TrimSpace(string(creds.Data["AWS_ACCESS_KEY_ID"]))
	if accessKey == "" {
		accessKey = strings.TrimSpace(string(creds.Data["username"]))
	}
	secretKey := strings.TrimSpace(string(creds.Data["AWS_SECRET_ACCESS_KEY"]))
	if secretKey == "" {
		secretKey = strings.TrimSpace(string(creds.Data["password"]))
	}
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("credentials secret %s/%s missing access/secret key fields", cs.mounter.namespace, credsSecretName)
	}

	allowHTTP := strings.EqualFold(strings.TrimSpace(cfgMap.Data["awsAllowHTTP"]), "true")
	endpoint := strings.TrimSpace(cfgMap.Data["awsEndpoint"])

	delCfg := s3DeleteConfig{
		Endpoint:     endpoint,
		Region:       strings.TrimSpace(cfgMap.Data["awsDefaultRegion"]),
		AllowHTTP:    allowHTTP,
		AccessKeyID:  accessKey,
		SecretKey:    secretKey,
		UsePathStyle: true,
	}

	cs.logger.Infof("DeleteVolume: deleting S3 prefix s3://%s/%s (configMap=%s/%s)", loc.Bucket, loc.Prefix, cs.mounter.namespace, configMapName)
	return deleteS3Prefix(ctx, delCfg, loc)
}

func (cs *controllerService) findPersistentVolume(ctx context.Context, volumeID string) (*corev1.PersistentVolume, error) {
	// Fast path: PV name is often equal to the CSI volume handle we use.
	var pv corev1.PersistentVolume
	if err := cs.mounter.client.Get(ctx, types.NamespacedName{Name: volumeID}, &pv); err == nil {
		return &pv, nil
	}

	// Slow path: list and match by volumeHandle.
	var pvs corev1.PersistentVolumeList
	if err := cs.mounter.client.List(ctx, &pvs); err != nil {
		return nil, err
	}
	for i := range pvs.Items {
		item := &pvs.Items[i]
		if item.Spec.CSI != nil && item.Spec.CSI.VolumeHandle == volumeID {
			return item, nil
		}
	}
	return nil, nil
}
