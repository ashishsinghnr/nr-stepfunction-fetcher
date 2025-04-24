package stepfunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

type Fetcher struct {
	sfnClient  *sfn.Client
	logsClient *cloudwatchlogs.Client
}

func NewFetcher(ctx context.Context, region string) (*Fetcher, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Fetcher{
		sfnClient:  sfn.NewFromConfig(cfg),
		logsClient: cloudwatchlogs.NewFromConfig(cfg),
	}, nil
}

func (f *Fetcher) ListStateMachines(ctx context.Context) ([]StateMachine, error) {
	var stateMachines []StateMachine
	input := &sfn.ListStateMachinesInput{}

	paginator := sfn.NewListStateMachinesPaginator(f.sfnClient, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list state machines: %w", err)
		}

		for _, sm := range page.StateMachines {
			details, err := f.getStateMachineDetails(ctx, *sm.StateMachineArn)
			if err != nil {
				fmt.Printf("Warning: Failed to get details for %s: %v\n", *sm.StateMachineArn, err)
				continue
			}
			stateMachines = append(stateMachines, details)
		}
	}

	return stateMachines, nil
}

func (f *Fetcher) getStateMachineDetails(ctx context.Context, arn string) (StateMachine, error) {
	// Fetch state machine details
	input := &sfn.DescribeStateMachineInput{
		StateMachineArn: aws.String(arn),
	}

	result, err := f.sfnClient.DescribeStateMachine(ctx, input)
	if err != nil {
		return StateMachine{}, fmt.Errorf("failed to describe state machine %s: %w", arn, err)
	}

	// Log state machine type for debugging
	smType := string(result.Type)
	fmt.Printf("Debug: State machine %s type: %s\n", *result.Name, smType)

	// Parse state definitions
	states, err := parseDefinition(*result.Definition)
	if err != nil {
		return StateMachine{}, fmt.Errorf("failed to parse definition for %s: %w", arn, err)
	}

	// Fetch executions based on state machine type
	var executions []Execution
	if smType == "EXPRESS" {
		fmt.Printf("Debug: Fetching executions from CloudWatch Logs for Express Workflow %s\n", *result.Name)
		executions, err = f.getExpressExecutions(ctx, result)
		if err != nil {
			fmt.Printf("Warning: Failed to fetch Express Workflow executions for %s: %v\n", *result.Name, err)
			executions = []Execution{{
				ExecutionArn: "N/A",
				Status:       "Not supported (check CloudWatch Logs configuration)",
				StartTime:    "",
				EndTime:      "",
				Duration:     "N/A",
			}}
		}
	} else if smType == "STANDARD" {
		executions, err = f.getExecutions(ctx, arn)
		if err != nil {
			return StateMachine{}, fmt.Errorf("failed to fetch executions for %s: %w", arn, err)
		}
	} else {
		fmt.Printf("Warning: Unknown state machine type %s for %s\n", smType, *result.Name)
		executions = []Execution{{
			ExecutionArn: "N/A",
			Status:       fmt.Sprintf("Unknown state machine type: %s", smType),
			StartTime:    "",
			EndTime:      "",
			Duration:     "N/A",
		}}
	}

	return StateMachine{
		Name:         *result.Name,
		ARN:          *result.StateMachineArn,
		RoleARN:      *result.RoleArn,
		Definition:   *result.Definition,
		States:       states,
		Executions:   executions,
		CreationDate: result.CreationDate.Format(time.RFC3339),
		Type:         smType,
	}, nil
}

func (f *Fetcher) getExecutions(ctx context.Context, stateMachineArn string) ([]Execution, error) {
	var executions []Execution
	input := &sfn.ListExecutionsInput{
		StateMachineArn: aws.String(stateMachineArn),
		MaxResults:      50, // Adjust as needed
	}

	paginator := sfn.NewListExecutionsPaginator(f.sfnClient, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list executions: %w", err)
		}

		for _, exec := range page.Executions {
			descInput := &sfn.DescribeExecutionInput{
				ExecutionArn: exec.ExecutionArn,
			}
			descResult, err := f.sfnClient.DescribeExecution(ctx, descInput) // Fixed: f.sfnClient
			if err != nil {
				fmt.Printf("Warning: Failed to describe execution %s: %v\n", *exec.ExecutionArn, err)
				continue
			}

			endTime := ""
			duration := "N/A"
			if descResult.StopDate != nil {
				endTime = descResult.StopDate.Format(time.RFC3339)
				start, _ := time.Parse(time.RFC3339, descResult.StartDate.Format(time.RFC3339))
				end, _ := time.Parse(time.RFC3339, endTime)
				dur := end.Sub(start)
				duration = fmt.Sprintf("%v", dur)
			}

			executions = append(executions, Execution{
				ExecutionArn: *descResult.ExecutionArn,
				Status:       string(descResult.Status),
				StartTime:    descResult.StartDate.Format(time.RFC3339),
				EndTime:      endTime,
				Duration:     duration,
			})
		}
	}

	return executions, nil
}

func (f *Fetcher) getExpressExecutions(ctx context.Context, sm *sfn.DescribeStateMachineOutput) ([]Execution, error) {
	var executions []Execution

	// Check if logging is enabled
	if sm.LoggingConfiguration == nil || len(sm.LoggingConfiguration.Destinations) == 0 {
		return executions, fmt.Errorf("logging not enabled for Express Workflow %s", *sm.Name)
	}

	// Get the CloudWatch Log Group
	logGroupArn := sm.LoggingConfiguration.Destinations[0].CloudWatchLogsLogGroup.LogGroupArn
	if logGroupArn == nil {
		return executions, fmt.Errorf("no CloudWatch Log Group configured for %s", *sm.Name)
	}

	// Extract Log Group name from ARN
	logGroupName := strings.Split(*logGroupArn, ":log-group:")[1]
	logGroupName = strings.Split(logGroupName, ":")[0]
	fmt.Printf("Debug: Querying CloudWatch Log Group %s for %s\n", logGroupName, *sm.Name)

	// Query CloudWatch Logs for execution events
	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  aws.String(logGroupName),
		FilterPattern: aws.String(`{ $.eventType = "ExecutionStarted" || $.eventType = "ExecutionSucceeded" || $.eventType = "ExecutionFailed" || $.eventType = "ExecutionTimedOut" || $.eventType = "ExecutionAborted" }`),
		Limit:         aws.Int32(50),                                          // Adjust as needed
		StartTime:     aws.Int64(time.Now().Add(-24 * time.Hour).UnixMilli()), // Last 24 hours
	}

	result, err := f.logsClient.FilterLogEvents(ctx, input)
	if err != nil {
		return executions, fmt.Errorf("failed to query CloudWatch Logs for %s: %w", *sm.Name, err)
	}

	// Parse log events to extract execution details
	type logEvent struct {
		EventType    string `json:"eventType"`
		ExecutionArn string `json:"executionArn"`
		Timestamp    int64  `json:"timestamp"`
		Status       string `json:"status,omitempty"`
	}

	executionMap := make(map[string]*Execution)
	for _, event := range result.Events {
		var log logEvent
		if err := json.Unmarshal([]byte(*event.Message), &log); err != nil {
			fmt.Printf("Warning: Failed to parse log event for %s: %v\n", *sm.Name, err)
			continue
		}

		timestamp := time.UnixMilli(log.Timestamp).Format(time.RFC3339)
		if _, exists := executionMap[log.ExecutionArn]; !exists && log.EventType == "ExecutionStarted" {
			executionMap[log.ExecutionArn] = &Execution{
				ExecutionArn: log.ExecutionArn,
				Status:       "RUNNING",
				StartTime:    timestamp,
				EndTime:      "",
				Duration:     "N/A",
			}
		} else if exec, exists := executionMap[log.ExecutionArn]; exists && strings.HasPrefix(log.EventType, "Execution") && log.EventType != "ExecutionStarted" {
			exec.Status = strings.Replace(log.EventType, "Execution", "", 1)
			exec.EndTime = timestamp
			start, _ := time.Parse(time.RFC3339, exec.StartTime)
			end, _ := time.Parse(time.RFC3339, exec.EndTime)
			exec.Duration = fmt.Sprintf("%v", end.Sub(start))
		}
	}

	for _, exec := range executionMap {
		executions = append(executions, *exec)
	}

	if len(executions) == 0 {
		fmt.Printf("Debug: No execution events found in CloudWatch Logs for %s\n", *sm.Name)
	} else {
		fmt.Printf("Debug: Found %d executions in CloudWatch Logs for %s\n", len(executions), *sm.Name)
	}

	return executions, nil
}

func parseDefinition(definition string) ([]State, error) {
	var aslDef struct {
		States map[string]map[string]interface{} `json:"States"`
	}

	if err := json.Unmarshal([]byte(definition), &aslDef); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ASL definition: %w", err)
	}

	var states []State
	for name, rawDef := range aslDef.States {
		stateType, _ := rawDef["Type"].(string)
		next, _ := rawDef["Next"].(string)
		end, _ := rawDef["End"].(bool)
		parameters, _ := rawDef["Parameters"].(map[string]interface{})

		states = append(states, State{
			Name:          name,
			Type:          stateType,
			Next:          next,
			End:           end,
			Parameters:    parameters,
			RawDefinition: rawDef,
		})
	}

	return states, nil
}
