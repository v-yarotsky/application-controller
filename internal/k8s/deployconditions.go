package k8s

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ConvertDeploymentConditionsToStandardForm(deployConditions []appsv1.DeploymentCondition) []metav1.Condition {
	conditions := make([]metav1.Condition, 0, len(deployConditions))
	for _, c := range deployConditions {
		conditions = append(conditions, metav1.Condition{
			Type:               string(c.Type),
			Status:             metav1.ConditionStatus(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: c.LastTransitionTime,
		})
	}
	return conditions
}
