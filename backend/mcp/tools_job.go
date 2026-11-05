package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Unified async-job tools: duihao_exec launches a command as an async job and
// always returns a jobId; duihao_exec_result polls it; duihao_interrupt
// stops it.
//
// WHICH toolset is registered is decided by the user's ExecMode setting, read
// once per server Start — the agent never picks a mode at call time:
//   - background: non-PTY SSH exec (real exit code, separate stdout/stderr).
//   - foreground: the command is typed into the user's live terminal and
//     completes on an idle-settle event or explicit EOF — never on an injected
//     end marker. Output is redraw-resolved screen text (see the foreground job registry), and the
//     set additionally ships duihao_capture / duihao_send_key so the agent
//     can see interactive prompts before answering them.
//
// The original exec_command / get_command_output / interrupt_command tools
// remain available only in background mode.

const (
	ModeBackground = "background"
	ModeForeground = "foreground"

	// fgJobPrefix marks a jobId owned by the foreground (PTY) runner so the
	// poll/interrupt dispatch can tell it apart from an SSH exec commandId.
	fgJobPrefix = "fg-"
	// bgCachePrefix marks a synchronously-completed background result cached
	// for duihao_exec_result.
	bgCachePrefix = "bg-"
)

// finishedTTL bounds how long a sync-completed background result stays cached
// and pollable before reaping.
const finishedTTL = 10 * time.Minute

// finishedBackground is a sync-completed background result cached so the
// unified result tool can read it through the same poll path as async jobs.
type finishedBackground struct {
	stdout    []byte
	stderr    []byte
	exitCode  int
	state     string
	expiresAt time.Time
}

// bgExecIn is the background-mode duihao_exec input. timeoutMs is the sync
// wait budget before the job goes async and a commandId is returned;
// hardLimitMs is the hard runtime cap after which the command is reported
// failed.
type bgExecIn struct {
	SessionID   string `json:"sessionId" jsonschema:"session id from list_sessions"`
	Command     string `json:"command" jsonschema:"command to execute"`
	TimeoutMs   int    `json:"timeoutMs,omitempty" jsonschema:"sync wait budget before returning an async commandId (default 20000)"`
	HardLimitMs int    `json:"hardLimitMs,omitempty" jsonschema:"hard runtime cap in ms (default 300000)"`
}

// fgExecIn is the foreground-mode duihao_exec input. Timeout semantics mirror
// the built-in AI panel's execute_command: the caller sets timeoutMs based on
// expected duration; on expiry the job moves to the pollable "timeout" state
// instead of being killed, and idleMs is how much terminal silence counts as
// completion.
type fgExecIn struct {
	SessionID string `json:"session_id" jsonschema:"session id from list_sessions"`
	Command   string `json:"command" jsonschema:"shell command to execute"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"maximum output collection time in milliseconds (default 60000); does not terminate the command"`
	IdleMs    int    `json:"idle_ms,omitempty" jsonschema:"silence that completes collection in milliseconds (default 800)"`
	HeadLines *int   `json:"head_lines,omitempty" jsonschema:"head lines to keep when truncating output (default 50; zero allowed)"`
	TailLines *int   `json:"tail_lines,omitempty" jsonschema:"tail lines to keep when truncating output (default 300; zero allowed)"`
}

// DefaultFgTimeout mirrors the built-in AI panel's execute_command default
// (60s): the agent is expected to size the timeout per command.
const DefaultFgTimeout = 60 * time.Second

type terminalOutputOptions struct{ head, tail int }

func validateLineLimits(head, tail *int) error {
	if (head != nil && *head < 0) || (tail != nil && *tail < 0) {
		return fmt.Errorf("head_lines and tail_lines must be non-negative")
	}
	return nil
}

func (s *Server) rememberOutputOptions(id string, head, tail *int, defaultHead int) {
	opts := terminalOutputOptions{defaultHead, 300}
	if head != nil {
		opts.head = *head
	}
	if tail != nil {
		opts.tail = *tail
	}
	s.mu.Lock()
	if s.foregroundOutput == nil {
		s.foregroundOutput = make(map[string]terminalOutputOptions)
	}
	s.foregroundOutput[id] = opts
	s.mu.Unlock()
	time.AfterFunc(finishedTTL, func() {
		s.mu.Lock()
		delete(s.foregroundOutput, id)
		s.mu.Unlock()
	})
}

func (s *Server) shapeForegroundOutput(id, output string, head, tail *int) (string, bool) {
	s.mu.Lock()
	opts, ok := s.foregroundOutput[id]
	s.mu.Unlock()
	if !ok {
		opts = terminalOutputOptions{50, 300}
	}
	if head != nil {
		opts.head = *head
	}
	if tail != nil {
		opts.tail = *tail
	}
	return TruncateOutputLines(output, opts.head, opts.tail)
}

type duihaoExecOut struct {
	JobID     string `json:"job_id"`
	Async     bool   `json:"async,omitempty"`
	Mode      string `json:"mode"`
	State     string `json:"state,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated,omitempty"`
}

type duihaoResultIn struct {
	JobID     string `json:"job_id" jsonschema:"job id returned by duihao_exec"`
	Offset    int    `json:"offset,omitempty" jsonschema:"byte offset to resume stdout from (default 0)"`
	MaxBytes  int    `json:"max_bytes,omitempty" jsonschema:"maximum raw output bytes per page (default and maximum 65536)"`
	HeadLines *int   `json:"head_lines,omitempty" jsonschema:"override head lines kept per output page"`
	TailLines *int   `json:"tail_lines,omitempty" jsonschema:"override tail lines kept per output page"`
}

type duihaoResultOut struct {
	JobID      string `json:"job_id"`
	Mode       string `json:"mode,omitempty"`
	State      string `json:"state"` // running | exited | error | killed | timeout | session_closed | unknown
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code"`
	NextOffset int    `json:"next_offset"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type duihaoInterruptIn struct {
	JobID  string `json:"job_id" jsonschema:"job id from duihao_exec"`
	Signal string `json:"signal,omitempty" jsonschema:"SIGINT (default), SIGTERM or SIGKILL"`
}

type duihaoCaptureIn struct {
	SessionID string `json:"session_id" jsonschema:"session id from list_sessions"`
	TailLines int    `json:"tail_lines,omitempty" jsonschema:"max screen lines to return, counting back from the last non-blank line (default 200)"`
}

type duihaoCaptureOut struct {
	SessionID string `json:"session_id"`
	Screen    string `json:"screen"`
}

type duihaoSendKeyIn struct {
	SessionID string `json:"session_id" jsonschema:"session id from list_sessions"`
	Input     string `json:"input,omitempty" jsonschema:"text to type into the terminal"`
	Control   string `json:"control,omitempty" jsonschema:"ctrl_c | ctrl_d | enter — sends the control key instead of text"`
	SendEnter *bool  `json:"send_enter,omitempty" jsonschema:"append Enter after text (default true)"`
}

type duihaoSendKeyOut struct {
	Sent bool `json:"sent"`
}

// execMode resolves the configured execution mode; unknown/empty falls back to
// background so hand-edited settings never strand the agent with a mode the
// app can't honor.
func (s *Server) execMode() string {
	if s.env.ExecMode == nil {
		return ModeBackground
	}
	if s.env.ExecMode() == ModeForeground {
		return ModeForeground
	}
	return ModeBackground
}

// registerJobTools installs the duihao_* toolset for the configured mode.
func (s *Server) registerJobTools(srv *mcp.Server) {
	if s.execMode() == ModeForeground {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "duihao_exec",
			Description: "Run a command by typing it into the user's live terminal. Works on any connected terminal (SSH, local, telnet, serial). " +
				"Completion is detected when the terminal goes quiet (idle_ms, default 800) or the session closes — there is no exit code. " +
				"Set timeout_ms from the expected duration (see its schema). Returns a job_id; poll with duihao_exec_result(job_id), stop with duihao_interrupt(job_id). " +
				"Check the screen with duihao_capture first when the command may hit an interactive prompt, and answer prompts with duihao_send_key.",
		}, s.toolDuihaoExecFg)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "duihao_exec_result",
			Description: "Fetch output (and status) of a job started by duihao_exec, by job_id. Supports paging via offset. Output is the screen-resolved terminal text — no escape sequences, no duplicated redraw lines.",
		}, s.toolDuihaoResult)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "duihao_interrupt",
			Description: "Stop a still-running job started by duihao_exec (detaches the job watcher; the command itself may need ctrl_c via duihao_send_key).",
		}, s.toolDuihaoInterrupt)
		mcp.AddTool(srv, &mcp.Tool{
			Name: "duihao_capture",
			Description: "Take an instant snapshot of a live session's terminal screen — what the user currently sees, redraw-resolved, no escape sequences. " +
				"Read-only. Use to check the current state, to SEE an interactive prompt (password, y/n, confirmation) before answering it with duihao_send_key, " +
				"or to inspect a still-running command's progress.",
		}, s.toolDuihaoCapture)
		mcp.AddTool(srv, &mcp.Tool{
			Name: "duihao_collect_output",
			Description: "Wait for further output from a live terminal without sending a command. " +
				"Returns a job_id to poll with duihao_exec_result. Uses the same idle and timeout rules as duihao_exec.",
		}, s.toolDuihaoCollect)
		mcp.AddTool(srv, &mcp.Tool{
			Name: "duihao_send_key",
			Description: "Type text or send a control key into a live session's terminal. ONLY send keys when you can SEE the prompt via duihao_capture first — never blind-type. " +
				"control=ctrl_c interrupts the running command; ctrl_d sends EOF; enter presses Enter alone. With input, send_enter (default true) appends Enter.",
		}, s.toolDuihaoSendKey)
		return
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "duihao_exec",
		Description: "Run a command on a connected SSH session over a separate non-PTY exec channel: real exit code, separate stdout/stderr, the user's terminal is untouched. " +
			"SSH sessions only. Waits up to timeoutMs, then returns an async job_id to poll. Poll with duihao_exec_result(job_id), stop with duihao_interrupt(job_id).",
	}, s.toolDuihaoExecBg)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "duihao_exec_result",
		Description: "Fetch output (and status/exit code) of a job started by duihao_exec, by job_id. Supports paging via offset.",
	}, s.toolDuihaoResult)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "duihao_interrupt",
		Description: "Stop a still-running job started by duihao_exec (SIGINT/SIGTERM/SIGKILL for background jobs).",
	}, s.toolDuihaoInterrupt)
}

// validateExec checks the fields shared by both modes' duihao_exec inputs.
func validateExec(sessionID, command string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("sessionId is required")
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}

func (s *Server) toolDuihaoExecBg(ctx context.Context, req *mcp.CallToolRequest, in bgExecIn) (*mcp.CallToolResult, duihaoExecOut, error) {
	if err := validateExec(in.SessionID, in.Command); err != nil {
		return nil, duihaoExecOut{}, err
	}
	risk := classifyCommand(in.Command)
	if classifyDownloadPipe(in.Command) {
		risk = RiskDangerous
	}
	if err := s.gateExec(ctx, req, in.SessionID, in.Command, risk); err != nil {
		return nil, duihaoExecOut{}, err
	}
	return s.execBackground(ctx, req, in.SessionID, in.Command, in.TimeoutMs, in.HardLimitMs)
}

func (s *Server) toolDuihaoExecFg(ctx context.Context, req *mcp.CallToolRequest, in fgExecIn) (*mcp.CallToolResult, duihaoExecOut, error) {
	if err := validateExec(in.SessionID, in.Command); err != nil {
		return nil, duihaoExecOut{}, err
	}
	risk := classifyCommand(in.Command)
	if classifyDownloadPipe(in.Command) {
		risk = RiskDangerous
	}
	if err := s.gateExec(ctx, req, in.SessionID, in.Command, risk); err != nil {
		return nil, duihaoExecOut{}, err
	}
	if err := validateLineLimits(in.HeadLines, in.TailLines); err != nil {
		return nil, duihaoExecOut{}, err
	}
	_, out, err := s.execForeground(ctx, req, in.SessionID, in.Command, in.TimeoutMs, in.IdleMs)
	if err == nil {
		s.rememberOutputOptions(out.JobID, in.HeadLines, in.TailLines, 50)
	}
	return nil, out, err
}

// execBackground wraps the existing SSH non-PTY executor behind the unified
// job schema. An async commandId is returned as-is; a sync-completed result is
// minted a cached jobId so the client always reads through duihao_exec_result.
func (s *Server) execBackground(ctx context.Context, req *mcp.CallToolRequest, sessionID, command string, timeoutMs, hardLimitMs int) (*mcp.CallToolResult, duihaoExecOut, error) {
	exec, ok := s.env.Sessions(sessionID)
	if !ok {
		return nil, duihaoExecOut{Mode: ModeBackground}, fmt.Errorf("session %s not found (use list_sessions)", sessionID)
	}

	syncTimeout := timeoutMs
	if syncTimeout <= 0 {
		syncTimeout = int(DefaultSyncTimeout / time.Millisecond)
	}
	hardTimeout := hardLimitMs
	if hardTimeout <= 0 {
		hardTimeout = int(DefaultCommandTimeout / time.Millisecond)
	}
	if hardTimeout < syncTimeout {
		hardTimeout = syncTimeout
	}

	stdout, stderr, exitCode, cmdID, err := exec.MCPExec(command, syncTimeout, hardTimeout, DefaultMaxOutputBytes)
	out := duihaoExecOut{Mode: ModeBackground, ExitCode: exitCode}
	s.audit(ctx, req, "duihao_exec", sessionID, command, &exitCode, err)

	if cmdID != "" {
		out.JobID = cmdID
		out.Async = true
		out.State = "running"
		return nil, out, nil
	}

	jobID := bgCachePrefix + uuid.New().String()
	s.cacheFinishedBackground(jobID, stdout, stderr, exitCode)
	out.JobID = jobID
	out.State = "exited"
	out.Stdout = string(stdout)
	out.Stderr = string(stderr)
	out.Truncated = len(stdout) >= DefaultMaxOutputBytes || len(stderr) >= DefaultMaxOutputBytes
	return nil, out, err
}

// execForeground starts a live-PTY tap job; completion is an idle/EOF event.
// timeoutMs mirrors the built-in AI panel's timeout: it is the max wait; on
// expiry the job moves to the pollable "timeout" state rather than being
// killed, so the agent can keep polling or interrupt explicitly.
func (s *Server) execForeground(ctx context.Context, req *mcp.CallToolRequest, sessionID, command string, timeoutMs, idleMs int) (*mcp.CallToolResult, duihaoExecOut, error) {
	if s.env.Foreground == nil {
		return nil, duihaoExecOut{Mode: ModeForeground}, fmt.Errorf("foreground mode is not available on this build")
	}
	wait := timeoutMs
	if wait <= 0 {
		wait = int(DefaultFgTimeout / time.Millisecond)
	}
	if idleMs <= 0 {
		idleMs = int(DefaultIdleSettle / time.Millisecond)
	}
	jobID, err := s.env.Foreground.StartForeground(sessionID, command, idleMs, wait)
	out := duihaoExecOut{Mode: ModeForeground, JobID: jobID, Async: true, State: "running"}
	s.audit(ctx, req, "duihao_exec", sessionID, command, nil, err)
	if err != nil {
		out.State = "error"
		return nil, out, err
	}
	return nil, out, nil
}

func (s *Server) toolDuihaoResult(ctx context.Context, req *mcp.CallToolRequest, in duihaoResultIn) (*mcp.CallToolResult, duihaoResultOut, error) {
	if strings.TrimSpace(in.JobID) == "" {
		return nil, duihaoResultOut{}, fmt.Errorf("job_id is required")
	}

	if in.Offset < 0 {
		return nil, duihaoResultOut{}, fmt.Errorf("offset must be non-negative")
	}
	if in.MaxBytes < 0 || in.MaxBytes > DefaultMaxOutputBytes {
		return nil, duihaoResultOut{}, fmt.Errorf("max_bytes must be between 1 and %d", DefaultMaxOutputBytes)
	}
	if in.MaxBytes == 0 {
		in.MaxBytes = DefaultMaxOutputBytes
	}
	if err := validateLineLimits(in.HeadLines, in.TailLines); err != nil {
		return nil, duihaoResultOut{}, err
	}

	// Foreground runner owns fg- prefixed jobs.
	if s.env.Foreground != nil && strings.HasPrefix(in.JobID, fgJobPrefix) {
		stdout, stderr, exitCode, state, err := s.env.Foreground.PollForeground(in.JobID, in.Offset, in.MaxBytes)
		out := duihaoResultOut{
			JobID:      in.JobID,
			Mode:       ModeForeground,
			State:      state,
			Stdout:     string(stdout),
			Stderr:     string(stderr),
			ExitCode:   exitCode,
			NextOffset: in.Offset + len(stdout),
		}
		out.Stdout, out.Truncated = s.shapeForegroundOutput(in.JobID, out.Stdout, in.HeadLines, in.TailLines)
		s.audit(ctx, req, "duihao_exec_result", "", in.JobID, &exitCode, err)
		return nil, out, nil
	}

	// Cached sync-completed background result.
	if buf, ok := s.takeFinishedBackground(in.JobID, in.Offset, in.MaxBytes); ok {
		s.audit(ctx, req, "duihao_exec_result", "", in.JobID, &buf.ExitCode, nil)
		return nil, buf, nil
	}

	// Async SSH exec commandId owned by a session.
	sid, exec, ok := s.env.Commands(in.JobID)
	if !ok || exec == nil {
		return nil, duihaoResultOut{JobID: in.JobID, State: "unknown"}, fmt.Errorf("job %s not found or expired", in.JobID)
	}
	stdout, stderr, exitCode, state, err := exec.MCPLatestOutput(in.JobID, in.Offset, in.MaxBytes)
	out := duihaoResultOut{
		JobID:      in.JobID,
		Mode:       ModeBackground,
		State:      state,
		Stdout:     string(stdout),
		Stderr:     string(stderr),
		ExitCode:   exitCode,
		NextOffset: in.Offset + len(stdout),
	}
	s.audit(ctx, req, "duihao_exec_result", sid, "", &exitCode, err)
	return nil, out, nil
}

func (s *Server) toolDuihaoInterrupt(ctx context.Context, req *mcp.CallToolRequest, in duihaoInterruptIn) (*mcp.CallToolResult, struct{}, error) {
	if strings.TrimSpace(in.JobID) == "" {
		return nil, struct{}{}, fmt.Errorf("job_id is required")
	}
	sig := in.Signal
	if sig == "" {
		sig = "SIGINT"
	}
	if s.env.Foreground != nil && strings.HasPrefix(in.JobID, fgJobPrefix) {
		err := s.env.Foreground.InterruptForeground(in.JobID, sig)
		s.audit(ctx, req, "duihao_interrupt", "", in.JobID, nil, err)
		return nil, struct{}{}, err
	}
	_, exec, ok := s.env.Commands(in.JobID)
	if !ok || exec == nil {
		return nil, struct{}{}, fmt.Errorf("job %s not found or expired", in.JobID)
	}
	err := exec.MCPInterrupt(in.JobID, sig)
	s.audit(ctx, req, "duihao_interrupt", "", in.JobID, nil, err)
	return nil, struct{}{}, err
}

func (s *Server) toolDuihaoCapture(ctx context.Context, req *mcp.CallToolRequest, in duihaoCaptureIn) (*mcp.CallToolResult, duihaoCaptureOut, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, duihaoCaptureOut{}, fmt.Errorf("sessionId is required")
	}
	if s.env.Capture == nil {
		return nil, duihaoCaptureOut{}, fmt.Errorf("capture is not available on this build")
	}
	// Read-only, but still gated: confirm_all users want to see every access,
	// and the screen may hold secrets.
	if err := s.gateExec(ctx, req, in.SessionID, "duihao_capture", RiskRead); err != nil {
		return nil, duihaoCaptureOut{}, err
	}
	if in.TailLines <= 0 {
		in.TailLines = 200
	}
	screen, err := s.env.Capture(in.SessionID, in.TailLines)
	s.audit(ctx, req, "duihao_capture", in.SessionID, "", nil, err)
	if err != nil {
		return nil, duihaoCaptureOut{}, err
	}
	return nil, duihaoCaptureOut{SessionID: in.SessionID, Screen: screen}, nil
}

func (s *Server) toolDuihaoSendKey(ctx context.Context, req *mcp.CallToolRequest, in duihaoSendKeyIn) (*mcp.CallToolResult, duihaoSendKeyOut, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, duihaoSendKeyOut{}, fmt.Errorf("sessionId is required")
	}
	if s.env.SendKey == nil {
		return nil, duihaoSendKeyOut{}, fmt.Errorf("send-key is not available on this build")
	}
	if in.Control == "" && in.Input == "" {
		return nil, duihaoSendKeyOut{}, fmt.Errorf("either input or control is required")
	}
	switch in.Control {
	case "", "ctrl_c", "ctrl_d", "enter":
	default:
		return nil, duihaoSendKeyOut{}, fmt.Errorf("unknown control %q (want ctrl_c|ctrl_d|enter)", in.Control)
	}
	// Typing into the live terminal is a write action; surface what will be
	// typed in the approval dialog.
	desc := in.Input
	if in.Control != "" {
		desc = fmt.Sprintf("[control:%s]", in.Control)
	}
	if err := s.gateExec(ctx, req, in.SessionID, "send-key: "+desc, RiskWrite); err != nil {
		return nil, duihaoSendKeyOut{}, err
	}
	err := s.env.SendKey(in.SessionID, in.Input, in.Control, in.SendEnter == nil || *in.SendEnter)
	s.audit(ctx, req, "duihao_send_key", in.SessionID, desc, nil, err)
	if err != nil {
		return nil, duihaoSendKeyOut{}, err
	}
	return nil, duihaoSendKeyOut{Sent: true}, nil
}

type unitermCollectIn struct {
	SessionID string `json:"session_id" jsonschema:"live terminal session id"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"maximum output collection time in milliseconds (default 30000)"`
	IdleMs    int    `json:"idle_ms,omitempty" jsonschema:"silence that completes collection in milliseconds (default 800)"`
	HeadLines *int   `json:"head_lines,omitempty" jsonschema:"head lines to keep when truncating output (default 100)"`
	TailLines *int   `json:"tail_lines,omitempty" jsonschema:"tail lines to keep when truncating output (default 300)"`
}

func (s *Server) toolDuihaoCollect(ctx context.Context, req *mcp.CallToolRequest, in unitermCollectIn) (*mcp.CallToolResult, duihaoExecOut, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, duihaoExecOut{}, fmt.Errorf("sessionId is required")
	}
	if s.env.Collect == nil {
		return nil, duihaoExecOut{}, fmt.Errorf("output collection is unavailable")
	}
	if err := s.gateExec(ctx, req, in.SessionID, "duihao_collect_output", RiskRead); err != nil {
		return nil, duihaoExecOut{}, err
	}
	if in.TimeoutMs <= 0 {
		in.TimeoutMs = 30000
	}
	if in.IdleMs <= 0 {
		in.IdleMs = int(DefaultIdleSettle / time.Millisecond)
	}
	if err := validateLineLimits(in.HeadLines, in.TailLines); err != nil {
		return nil, duihaoExecOut{}, err
	}
	id, err := s.env.Collect(in.SessionID, in.IdleMs, in.TimeoutMs)
	s.audit(ctx, req, "duihao_collect_output", in.SessionID, "", nil, err)
	if err != nil {
		return nil, duihaoExecOut{}, err
	}
	s.rememberOutputOptions(id, in.HeadLines, in.TailLines, 100)
	return nil, duihaoExecOut{JobID: id, Mode: ModeForeground, State: "running", Async: true}, nil
}

// ── sync-completed background result cache ──────────────────────────────
//
// A background command that finishes inside the sync budget still gets a
// jobId (the unified contract always returns one); its result lives here until
// polled or TTL-expired. The map is small and short-lived.

func (s *Server) cacheFinishedBackground(jobID string, stdout, stderr []byte, exitCode int) {
	s.mu.Lock()
	if s.finishedBg == nil {
		s.finishedBg = map[string]finishedBackground{}
	}
	s.finishedBg[jobID] = finishedBackground{
		stdout:    append([]byte(nil), stdout...),
		stderr:    append([]byte(nil), stderr...),
		exitCode:  exitCode,
		state:     "exited",
		expiresAt: time.Now().Add(finishedTTL),
	}
	s.mu.Unlock()
}

func (s *Server) takeFinishedBackground(jobID string, offset, maxBytes int) (duihaoResultOut, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.finishedBg[jobID]
	if !ok {
		return duihaoResultOut{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(s.finishedBg, jobID)
		return duihaoResultOut{}, false
	}
	out := entry.stdout
	if offset > 0 {
		if offset >= len(out) {
			out = nil
		} else {
			out = out[offset:]
		}
	}
	if len(out) > maxBytes {
		out = out[:maxBytes]
	}
	return duihaoResultOut{
		JobID:      jobID,
		Mode:       ModeBackground,
		State:      entry.state,
		Stdout:     string(out),
		Stderr:     string(truncateBytes(entry.stderr, maxBytes)),
		ExitCode:   entry.exitCode,
		NextOffset: offset + len(out),
	}, true
}

// truncateBytes caps a byte slice at max, cutting at a newline when possible
// and appending a marker. Local to the mcp package (the session package has
// its own copy for the SSH path).
func truncateBytes(b []byte, max int) []byte {
	if max <= 0 || len(b) <= max {
		return b
	}
	const marker = "\n…[duihao: output truncated]"
	trunc := b[:max]
	if i := strings.LastIndexByte(string(trunc), '\n'); i > max/2 {
		trunc = trunc[:i]
	}
	return append(trunc, []byte(marker)...)
}
