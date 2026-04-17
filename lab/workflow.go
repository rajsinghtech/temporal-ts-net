package lab

import (
	"time"

	"go.temporal.io/sdk/workflow"
)

func HealthCheckWorkflow(ctx workflow.Context) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	var act *Activities

	var metrics string
	if err := workflow.ExecuteActivity(ctx, act.FetchMetrics).Get(ctx, &metrics); err != nil {
		return "", err
	}

	var summary string
	if err := workflow.ExecuteActivity(ctx, act.AnalyzeMetrics, metrics).Get(ctx, &summary); err != nil {
		return "", err
	}

	return summary, nil
}
