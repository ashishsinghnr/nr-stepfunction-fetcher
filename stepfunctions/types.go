package stepfunctions

// StateMachine represents a Step Functions state machine
type StateMachine struct {
	Name         string
	ARN          string
	RoleARN      string
	Definition   string
	States       []State
	Executions   []Execution
	CreationDate string
	Type         string
}

// State represents an individual state in the state machine
type State struct {
	Name          string
	Type          string
	Next          string
	End           bool
	Parameters    map[string]interface{}
	RawDefinition map[string]interface{}
}

// Execution represents an execution of a state machine
type Execution struct {
	ExecutionArn string
	Status       string
	StartTime    string
	EndTime      string
	Duration     string // Human-readable duration (e.g., "1m30s")
}
