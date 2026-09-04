package tool

import (
	"context"
	"fmt"
	"strings"

	"kite/trigger"
)

type TriggerCreate struct {
	DataDir   string
	Scheduler *trigger.Scheduler
}

func (t *TriggerCreate) Name() string { return "trigger_create" }
func (t *TriggerCreate) Description() string {
	return "Create a scheduled trigger or reminder. The schedule must be a valid cron expression (e.g., '0 9 * * *' for daily at 9am, '*/30 * * * *' for every 30 minutes, '0 8 * * 1-5' for weekdays at 8am). One-shot triggers fire once and are deleted."
}
func (t *TriggerCreate) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"schedule":  strParam("Cron expression for when this fires (minute hour day-of-month month day-of-week)."),
		"prompt":    strParam("The task description. This prompt will be sent to the agent when the trigger fires."),
		"one_shot":  strParam("Set to 'true' for a one-time trigger (default: 'false' for recurring)."),
		"agent_id":  strParam("The agent ID that should handle this trigger. If empty, the current agent is used."),
	}, []string{"schedule", "prompt"})
}

func (t *TriggerCreate) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	schedule := getStringArg(args, "schedule")
	prompt := getStringArg(args, "prompt")
	oneShot := getStringArg(args, "one_shot") == "true"
	agentID := getStringArg(args, "agent_id")

	if schedule == "" {
		return "Error: schedule is required (cron expression like '0 9 * * *')", nil
	}
	if prompt == "" {
		return "Error: prompt is required", nil
	}

	tr, err := t.Scheduler.AddTrigger(userID, agentID, schedule, prompt, oneShot)
	if err != nil {
		return fmt.Sprintf("Error creating trigger: %v", err), nil
	}
	return fmt.Sprintf("Trigger created:\n  id: %s\n  schedule: %s\n  next_fire: %s\n  one_shot: %v\n  prompt: %s",
		tr.ID, tr.Schedule, tr.NextFire, oneShot, prompt), nil
}

type TriggerList struct {
	DataDir   string
	Scheduler *trigger.Scheduler
}

func (t *TriggerList) Name() string        { return "trigger_list" }
func (t *TriggerList) Description() string { return "List all pending triggers/reminders for the current user." }
func (t *TriggerList) Parameters() map[string]interface{} {
	return params(map[string]interface{}{}, nil)
}

func (t *TriggerList) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	triggers, err := t.Scheduler.ListTriggers(userID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(triggers) == 0 {
		return "No active triggers.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d trigger(s):\n", len(triggers)))
	for _, tr := range triggers {
		sb.WriteString(fmt.Sprintf("  - id: %s\n    schedule: %s\n    next_fire: %s\n    prompt: %s\n    agent: %s\n",
			tr.ID, tr.Schedule, tr.NextFire, tr.Prompt, tr.AgentID))
	}
	return sb.String(), nil
}

type TriggerDelete struct {
	DataDir   string
	Scheduler *trigger.Scheduler
}

func (t *TriggerDelete) Name() string        { return "trigger_delete" }
func (t *TriggerDelete) Description() string { return "Delete a scheduled trigger/reminder by ID." }
func (t *TriggerDelete) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"trigger_id": strParam("ID of the trigger to delete."),
	}, []string{"trigger_id"})
}

func (t *TriggerDelete) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	triggerID := getStringArg(args, "trigger_id")
	if triggerID == "" {
		return "Error: trigger_id is required", nil
	}
	if err := t.Scheduler.DeleteTrigger(userID, triggerID); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	return fmt.Sprintf("Trigger '%s' deleted.", triggerID), nil
}
