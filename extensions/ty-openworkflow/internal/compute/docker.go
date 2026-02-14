package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DockerAdapter implements compute.Adapter for Docker containers.
type DockerAdapter struct {
	workDir    string
	webhookURL string
	network    string
	runs       map[string]*WorkflowRun
	mu         sync.RWMutex
}

// DockerConfig holds configuration for the Docker adapter.
type DockerConfig struct {
	WorkDir string `yaml:"work_dir"`
	Network string `yaml:"network"` // Docker network for containers
}

// NewDockerAdapter creates a new Docker container adapter.
func NewDockerAdapter(cfg DockerConfig) *DockerAdapter {
	workDir := cfg.WorkDir
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "ty-openworkflow-docker")
	}
	os.MkdirAll(workDir, 0755)

	return &DockerAdapter{
		workDir: workDir,
		network: cfg.Network,
		runs:    make(map[string]*WorkflowRun),
	}
}

func (d *DockerAdapter) Name() string {
	return "docker"
}

func (d *DockerAdapter) IsAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

func (d *DockerAdapter) SetWebhook(url string) {
	d.webhookURL = url
}

// Deploy creates a Docker image for the workflow.
func (d *DockerAdapter) Deploy(ctx context.Context, workflow *WorkflowDefinition) error {
	// Create workflow directory
	workflowDir := filepath.Join(d.workDir, "workflows", workflow.ID)
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("create workflow dir: %w", err)
	}

	// Generate Dockerfile and workflow code
	dockerfile, code := d.generateDockerfiles(workflow)

	// Write Dockerfile
	dockerfilePath := filepath.Join(workflowDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}

	// Write workflow code
	var codePath string
	switch workflow.Runtime {
	case "node", "javascript":
		codePath = filepath.Join(workflowDir, "workflow.js")
	case "python":
		codePath = filepath.Join(workflowDir, "workflow.py")
	default:
		codePath = filepath.Join(workflowDir, "workflow.js")
	}

	if err := os.WriteFile(codePath, []byte(code), 0644); err != nil {
		return fmt.Errorf("write workflow code: %w", err)
	}

	// Write metadata
	metaPath := filepath.Join(workflowDir, "workflow.json")
	meta, _ := json.MarshalIndent(workflow, "", "  ")
	if err := os.WriteFile(metaPath, meta, 0644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	// Build Docker image
	imageName := fmt.Sprintf("ow-%s:%s", workflow.ID, workflow.Version)
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", imageName, workflowDir)
	cmd.Dir = workflowDir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build image: %s", stderr.String())
	}

	return nil
}

// generateDockerfiles creates the Dockerfile and workflow code for the given runtime.
func (d *DockerAdapter) generateDockerfiles(workflow *WorkflowDefinition) (string, string) {
	switch workflow.Runtime {
	case "node", "javascript":
		return d.generateNodeDockerfile(workflow)
	case "python":
		return d.generatePythonDockerfile(workflow)
	default:
		return d.generateNodeDockerfile(workflow)
	}
}

func (d *DockerAdapter) generateNodeDockerfile(workflow *WorkflowDefinition) (string, string) {
	dockerfile := `FROM node:20-alpine
WORKDIR /app
COPY workflow.js .
CMD ["node", "workflow.js"]
`

	code := fmt.Sprintf(`#!/usr/bin/env node
// OpenWorkflow Docker Runner: %s v%s

const fs = require('fs');
const path = require('path');

const WORKFLOW_ID = "%s";
const RUN_ID = process.env.RUN_ID || '';
const STATE_DIR = process.env.STATE_DIR || '/state';
const WEBHOOK_URL = process.env.WEBHOOK_URL || '';

function getStateFile() {
  return path.join(STATE_DIR, RUN_ID + '.json');
}

function loadState() {
  try {
    return JSON.parse(fs.readFileSync(getStateFile(), 'utf8'));
  } catch {
    return { steps: {} };
  }
}

function saveState(state) {
  fs.mkdirSync(STATE_DIR, { recursive: true });
  fs.writeFileSync(getStateFile(), JSON.stringify(state, null, 2));
}

async function step(name, fn) {
  const state = loadState();
  if (state.steps[name] && state.steps[name].status === 'completed') {
    return state.steps[name].output;
  }

  const startedAt = new Date().toISOString();
  try {
    const output = await fn();
    state.steps[name] = {
      status: 'completed',
      output,
      startedAt,
      completedAt: new Date().toISOString()
    };
    saveState(state);
    return output;
  } catch (error) {
    state.steps[name] = {
      status: 'failed',
      error: error.message,
      startedAt,
      completedAt: new Date().toISOString()
    };
    saveState(state);
    throw error;
  }
}

async function sleep(name, durationMs) {
  const state = loadState();
  const sleepKey = name + ':sleep';

  if (state.steps[sleepKey] && state.steps[sleepKey].status === 'completed') {
    return;
  }

  if (state.steps[sleepKey] && state.steps[sleepKey].status === 'sleeping') {
    const wakeAt = new Date(state.steps[sleepKey].wakeAt);
    if (new Date() >= wakeAt) {
      state.steps[sleepKey].status = 'completed';
      state.steps[sleepKey].completedAt = new Date().toISOString();
      saveState(state);
      return;
    }
    const remaining = wakeAt - new Date();
    await new Promise(resolve => setTimeout(resolve, remaining));
    state.steps[sleepKey].status = 'completed';
    state.steps[sleepKey].completedAt = new Date().toISOString();
    saveState(state);
    return;
  }

  const wakeAt = new Date(Date.now() + durationMs);
  state.steps[sleepKey] = {
    status: 'sleeping',
    wakeAt: wakeAt.toISOString(),
    startedAt: new Date().toISOString()
  };
  saveState(state);

  await new Promise(resolve => setTimeout(resolve, durationMs));

  state.steps[sleepKey].status = 'completed';
  state.steps[sleepKey].completedAt = new Date().toISOString();
  saveState(state);
}

// User workflow code
%s

async function main() {
  const input = JSON.parse(process.env.INPUT || '{}');

  try {
    const output = await workflow(input, { step, sleep });
    console.log(JSON.stringify({ status: 'completed', output }));

    if (WEBHOOK_URL) {
      const https = require('https');
      const http = require('http');
      const url = new URL(WEBHOOK_URL);
      const client = url.protocol === 'https:' ? https : http;
      const req = client.request(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }});
      req.write(JSON.stringify({ runId: RUN_ID, status: 'completed', output }));
      req.end();
    }
  } catch (error) {
    console.error(JSON.stringify({ status: 'failed', error: error.message }));
    process.exit(1);
  }
}

main();
`, workflow.Name, workflow.Version, workflow.ID, workflow.Code)

	return dockerfile, code
}

func (d *DockerAdapter) generatePythonDockerfile(workflow *WorkflowDefinition) (string, string) {
	dockerfile := `FROM python:3.12-alpine
WORKDIR /app
COPY workflow.py .
CMD ["python3", "workflow.py"]
`

	code := fmt.Sprintf(`#!/usr/bin/env python3
# OpenWorkflow Docker Runner: %s v%s

import os
import json
import time
import urllib.request
from datetime import datetime
from pathlib import Path

WORKFLOW_ID = "%s"
RUN_ID = os.environ.get("RUN_ID", "")
STATE_DIR = Path(os.environ.get("STATE_DIR", "/state"))
WEBHOOK_URL = os.environ.get("WEBHOOK_URL", "")

def get_state_file():
    return STATE_DIR / f"{RUN_ID}.json"

def load_state():
    try:
        with open(get_state_file()) as f:
            return json.load(f)
    except:
        return {"steps": {}}

def save_state(state):
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    with open(get_state_file(), "w") as f:
        json.dump(state, f, indent=2)

def step(name, fn):
    state = load_state()
    if name in state["steps"] and state["steps"][name].get("status") == "completed":
        return state["steps"][name]["output"]

    started_at = datetime.now().isoformat()
    try:
        output = fn()
        state["steps"][name] = {
            "status": "completed",
            "output": output,
            "startedAt": started_at,
            "completedAt": datetime.now().isoformat()
        }
        save_state(state)
        return output
    except Exception as e:
        state["steps"][name] = {
            "status": "failed",
            "error": str(e),
            "startedAt": started_at,
            "completedAt": datetime.now().isoformat()
        }
        save_state(state)
        raise

def sleep_step(name, duration_seconds):
    state = load_state()
    sleep_key = f"{name}:sleep"

    if sleep_key in state["steps"]:
        step_state = state["steps"][sleep_key]
        if step_state.get("status") == "completed":
            return
        if step_state.get("status") == "sleeping":
            wake_at = datetime.fromisoformat(step_state["wakeAt"])
            now = datetime.now()
            if now >= wake_at:
                state["steps"][sleep_key]["status"] = "completed"
                state["steps"][sleep_key]["completedAt"] = now.isoformat()
                save_state(state)
                return
            remaining = (wake_at - now).total_seconds()
            time.sleep(remaining)
            state["steps"][sleep_key]["status"] = "completed"
            state["steps"][sleep_key]["completedAt"] = datetime.now().isoformat()
            save_state(state)
            return

    wake_at = datetime.now().timestamp() + duration_seconds
    state["steps"][sleep_key] = {
        "status": "sleeping",
        "wakeAt": datetime.fromtimestamp(wake_at).isoformat(),
        "startedAt": datetime.now().isoformat()
    }
    save_state(state)
    time.sleep(duration_seconds)
    state["steps"][sleep_key]["status"] = "completed"
    state["steps"][sleep_key]["completedAt"] = datetime.now().isoformat()
    save_state(state)

class WorkflowContext:
    def __init__(self):
        self.step = step
        self.sleep = sleep_step

# User workflow code
%s

def main():
    input_data = json.loads(os.environ.get("INPUT", "{}"))
    ctx = WorkflowContext()

    try:
        output = workflow(input_data, ctx)
        print(json.dumps({"status": "completed", "output": output}))

        if WEBHOOK_URL:
            req = urllib.request.Request(
                WEBHOOK_URL,
                data=json.dumps({"runId": RUN_ID, "status": "completed", "output": output}).encode(),
                headers={"Content-Type": "application/json"},
                method="POST"
            )
            urllib.request.urlopen(req)
    except Exception as e:
        print(json.dumps({"status": "failed", "error": str(e)}))
        exit(1)

if __name__ == "__main__":
    main()
`, workflow.Name, workflow.Version, workflow.ID, workflow.Code)

	return dockerfile, code
}

// Start initiates a new workflow run in a Docker container.
func (d *DockerAdapter) Start(ctx context.Context, workflowID string, input map[string]any) (*WorkflowRun, error) {
	// Generate run ID
	runID := fmt.Sprintf("%s-%d", workflowID, time.Now().UnixNano())

	// Create run directory for state persistence
	runDir := filepath.Join(d.workDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}

	// Load workflow metadata
	workflowDir := filepath.Join(d.workDir, "workflows", workflowID)
	metaPath := filepath.Join(workflowDir, "workflow.json")
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read workflow metadata: %w", err)
	}

	var workflow WorkflowDefinition
	if err := json.Unmarshal(metaData, &workflow); err != nil {
		return nil, fmt.Errorf("parse workflow metadata: %w", err)
	}

	// Build docker run command
	imageName := fmt.Sprintf("ow-%s:%s", workflowID, workflow.Version)
	containerName := fmt.Sprintf("ow-run-%s", runID)
	inputJSON, _ := json.Marshal(input)

	args := []string{
		"run", "--rm",
		"--name", containerName,
		"-v", runDir + ":/state",
		"-e", "RUN_ID=" + runID,
		"-e", "INPUT=" + string(inputJSON),
		"-e", "WEBHOOK_URL=" + d.webhookURL,
	}

	if d.network != "" {
		args = append(args, "--network", d.network)
	}

	args = append(args, imageName)

	// Create run record
	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: workflowID,
		Status:     StatusRunning,
		Input:      input,
		StartedAt:  time.Now(),
		Metadata:   map[string]string{"container": containerName},
	}

	d.mu.Lock()
	d.runs[runID] = run
	d.mu.Unlock()

	// Start container async
	go func() {
		cmd := exec.CommandContext(ctx, "docker", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		d.mu.Lock()
		defer d.mu.Unlock()

		now := time.Now()
		run.CompletedAt = &now

		if err != nil {
			run.Status = StatusFailed
			run.Error = stderr.String()
			if run.Error == "" {
				run.Error = err.Error()
			}
		} else {
			// Parse output
			lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			if len(lines) > 0 {
				lastLine := lines[len(lines)-1]
				var result struct {
					Status string         `json:"status"`
					Output map[string]any `json:"output"`
					Error  string         `json:"error"`
				}
				if err := json.Unmarshal([]byte(lastLine), &result); err == nil {
					if result.Status == "completed" {
						run.Status = StatusCompleted
						run.Output = result.Output
					} else {
						run.Status = StatusFailed
						run.Error = result.Error
					}
				} else {
					run.Status = StatusCompleted
					run.Output = map[string]any{"raw": stdout.String()}
				}
			}
		}

		// Write final state
		stateData, _ := json.MarshalIndent(run, "", "  ")
		os.WriteFile(filepath.Join(runDir, "result.json"), stateData, 0644)
	}()

	return run, nil
}

// Status retrieves the current status of a workflow run.
func (d *DockerAdapter) Status(ctx context.Context, runID string) (*WorkflowRun, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	run, ok := d.runs[runID]
	if !ok {
		// Try to load from disk
		runDir := filepath.Join(d.workDir, "runs", runID)
		resultPath := filepath.Join(runDir, "result.json")
		data, err := os.ReadFile(resultPath)
		if err != nil {
			return nil, fmt.Errorf("run not found: %s", runID)
		}

		var diskRun WorkflowRun
		if err := json.Unmarshal(data, &diskRun); err != nil {
			return nil, fmt.Errorf("parse run result: %w", err)
		}
		return &diskRun, nil
	}

	return run, nil
}

// Cancel attempts to stop a running container.
func (d *DockerAdapter) Cancel(ctx context.Context, runID string) error {
	d.mu.RLock()
	run, ok := d.runs[runID]
	d.mu.RUnlock()

	if !ok {
		return fmt.Errorf("run not found")
	}

	containerName := run.Metadata["container"]
	if containerName == "" {
		return fmt.Errorf("container name not found")
	}

	cmd := exec.CommandContext(ctx, "docker", "stop", containerName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}

	d.mu.Lock()
	run.Status = StatusCanceled
	now := time.Now()
	run.CompletedAt = &now
	d.mu.Unlock()

	return nil
}

// Logs retrieves container logs.
func (d *DockerAdapter) Logs(ctx context.Context, runID string) ([]string, error) {
	d.mu.RLock()
	run, ok := d.runs[runID]
	d.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("run not found")
	}

	containerName := run.Metadata["container"]
	if containerName == "" {
		return nil, fmt.Errorf("container name not found")
	}

	cmd := exec.CommandContext(ctx, "docker", "logs", containerName)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("get logs: %w", err)
	}

	return strings.Split(stdout.String(), "\n"), nil
}

// Cleanup removes the run directory.
func (d *DockerAdapter) Cleanup(ctx context.Context, runID string) error {
	d.mu.Lock()
	delete(d.runs, runID)
	d.mu.Unlock()

	runDir := filepath.Join(d.workDir, "runs", runID)
	return os.RemoveAll(runDir)
}

// ListRuns returns recent workflow runs.
func (d *DockerAdapter) ListRuns(ctx context.Context, workflowID string, limit int) ([]*WorkflowRun, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var runs []*WorkflowRun
	for _, run := range d.runs {
		if workflowID == "" || run.WorkflowID == workflowID {
			runs = append(runs, run)
			if limit > 0 && len(runs) >= limit {
				break
			}
		}
	}

	return runs, nil
}
