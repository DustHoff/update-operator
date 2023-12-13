package helper

import (
	corev1 "k8s.io/api/core/v1"
)

func KubletReadyCondition(conditions []corev1.NodeCondition) bool {
	for _, condition := range conditions {
		if condition.Type == "Ready" && condition.Status == "True" && condition.Reason == "KubeletReady" {
			return true
		}
	}
	return false
}
