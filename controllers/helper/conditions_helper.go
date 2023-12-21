package helper

import (
	corev1 "k8s.io/api/core/v1"
)

func KubletReadyCondition(conditions []corev1.NodeCondition) (bool, *corev1.NodeCondition) {
	for _, condition := range conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == "True", &condition
		}
	}
	return false, nil
}
