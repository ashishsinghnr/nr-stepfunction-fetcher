package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"stepfunction-fetcher/stepfunctions"

	"github.com/olekukonko/tablewriter"
)

func main() {
	region := flag.String("region", "us-west-2", "AWS region")
	outputDir := flag.String("output-dir", "stepfunctions_state_definitions", "Directory to save state and execution definitions")
	flag.Parse()

	ctx := context.Background()

	_, stateMachines := initializeFetcherAndStateMachines(ctx, *region)
	createOutputDirectory(*outputDir)
	displayStateMachines(stateMachines)
	processStateMachines(ctx, stateMachines, *outputDir) // processStates + processExecutions
	fmt.Printf("State and execution definitions saved to %s\n", *outputDir)
	fmt.Println("Done.")
	fmt.Println("Note: For Express Workflows, ensure CloudWatch Logs are configured to fetch execution details as execution details are fetched from CloudWatch Logs..")
	fmt.Println("Note: For Standard Workflows, execution details are fetched directly from Step Functions.")
}

func initializeFetcherAndStateMachines(ctx context.Context, region string) (*stepfunctions.Fetcher, []stepfunctions.StateMachine) {
	fetcher, err := stepfunctions.NewFetcher(ctx, region)
	if err != nil {
		log.Fatalf("Failed to create fetcher: %v", err)
	}

	stateMachines, err := fetcher.ListStateMachines(ctx)
	if err != nil {
		log.Fatalf("Failed to list state machines: %v", err)
	}

	return fetcher, stateMachines
}

func createOutputDirectory(outputDir string) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}
}

func displayStateMachines(stateMachines []stepfunctions.StateMachine) {
	smTable := tablewriter.NewWriter(os.Stdout)
	smTable.SetHeader([]string{"Name", "ARN", "Type", "Role ARN", "Creation Date"})
	for _, sm := range stateMachines {
		smTable.Append([]string{
			sm.Name,
			sm.ARN,
			sm.Type,
			sm.RoleARN,
			sm.CreationDate,
		})
	}
	fmt.Println("State Machines:")
	smTable.Render()
	fmt.Println()
}

func processStateMachines(ctx context.Context, stateMachines []stepfunctions.StateMachine, outputDir string) {
	for _, sm := range stateMachines {
		processStates(sm, outputDir)
		processExecutions(sm, outputDir)
	}

	if err := saveToFile(stateMachines, filepath.Join(outputDir, "state_machines.json")); err != nil {
		log.Printf("Failed to save state machines: %v", err)
	}
}

func processStates(sm stepfunctions.StateMachine, outputDir string) {
	stateTable := tablewriter.NewWriter(os.Stdout)
	stateTable.SetHeader([]string{"State Name", "Type", "Next", "End", "Definition"})
	for _, state := range sm.States {
		rawDef, err := json.MarshalIndent(state.RawDefinition, "", "  ")
		if err != nil {
			log.Printf("Failed to marshal state definition for %s: %v", state.Name, err)
			continue
		}

		defStr := string(rawDef)
		if len(defStr) > 100 {
			defStr = defStr[:97] + "..."
		}

		stateTable.Append([]string{
			state.Name,
			state.Type,
			state.Next,
			fmt.Sprintf("%v", state.End),
			defStr,
		})

		if err := saveStateDefinition(outputDir, sm.Name, state.Name, rawDef); err != nil {
			log.Printf("Failed to save state definition for %s/%s: %v", sm.Name, state.Name, err)
		}
	}
	fmt.Printf("States for %s:\n", sm.Name)
	stateTable.Render()
	fmt.Println()
}

func processExecutions(sm stepfunctions.StateMachine, outputDir string) {
	execTable := tablewriter.NewWriter(os.Stdout)
	execTable.SetHeader([]string{"Execution ARN", "Status", "Start Time", "End Time", "Duration"})
	for _, exec := range sm.Executions {
		execTable.Append([]string{
			exec.ExecutionArn,
			exec.Status,
			exec.StartTime,
			exec.EndTime,
			exec.Duration,
		})

		if exec.ExecutionArn != "N/A" {
			execData, err := json.MarshalIndent(exec, "", "  ")
			if err != nil {
				log.Printf("Failed to marshal execution %s: %v", exec.ExecutionArn, err)
				continue
			}
			if err := saveExecutionDefinition(outputDir, sm.Name, exec.ExecutionArn, execData); err != nil {
				log.Printf("Failed to save execution %s: %v", exec.ExecutionArn, err)
			}
		}
	}
	fmt.Printf("Executions for %s:\n", sm.Name)
	execTable.Render()
	fmt.Println()
}

func saveStateDefinition(outputDir, smName, stateName string, definition []byte) error {
	safeStateName := sanitizeFileName(stateName)
	filePath := filepath.Join(outputDir, fmt.Sprintf("%s_%s.json", smName, safeStateName))
	return os.WriteFile(filePath, definition, 0644)
}

func saveExecutionDefinition(outputDir, smName, executionArn string, definition []byte) error {
	safeExecName := sanitizeFileName(strings.ReplaceAll(executionArn, ":", "_"))
	filePath := filepath.Join(outputDir, fmt.Sprintf("%s_execution_%s.json", smName, safeExecName))
	return os.WriteFile(filePath, definition, 0644)
}

func saveToFile(stateMachines []stepfunctions.StateMachine, filename string) error {
	data, err := json.MarshalIndent(stateMachines, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state machines: %w", err)
	}
	return os.WriteFile(filename, data, 0644)
}

func sanitizeFileName(name string) string {
	invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := name
	for _, char := range invalidChars {
		result = strings.ReplaceAll(result, char, "_")
	}
	return result
}
