package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ExecResult struct {
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
	Signal    string `json:"signal,omitempty"`
}

type Manager struct {
	dataDir           string
	alpineTarPath     string
	sharedRootfsPath  string
	maxOutputBytes    int
	commandTimeoutSec int
	maxDiskMB         int
	preinstallPkgs    []string
	dnsServers        []string
	mu                sync.Mutex
	initialized       map[string]bool
	rootfsReady       bool
}

func NewManager(dataDir, alpineTar, sharedRootfs string, maxOutputBytes, timeoutSec, maxDiskMB int, preinstall []string, dnsServers []string) *Manager {
	if len(dnsServers) == 0 {
		dnsServers = []string{"8.8.8.8", "1.1.1.1"}
	}
	return &Manager{
		dataDir:           dataDir,
		alpineTarPath:     alpineTar,
		sharedRootfsPath:  sharedRootfs,
		maxOutputBytes:    maxOutputBytes,
		commandTimeoutSec: timeoutSec,
		maxDiskMB:         maxDiskMB,
		preinstallPkgs:    preinstall,
		dnsServers:        dnsServers,
		initialized:       make(map[string]bool),
	}
}

func (m *Manager) HomeDir(userID string) string {
	return filepath.Join(m.dataDir, userID, "sandbox", "home")
}

func (m *Manager) InitSharedRootfs() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rootfsReady {
		return nil
	}
	if _, err := os.Stat(filepath.Join(m.sharedRootfsPath, "bin", "sh")); err == nil {
		m.rootfsReady = true
		return nil
	}
	os.RemoveAll(m.sharedRootfsPath)
	if err := os.MkdirAll(m.sharedRootfsPath, 0755); err != nil {
		return fmt.Errorf("mkdir shared rootfs: %w", err)
	}
	cmd := exec.Command("tar", "-xzf", m.alpineTarPath, "-C", m.sharedRootfsPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract alpine: %w: %s", err, string(out))
	}
	dnsConf := filepath.Join(m.sharedRootfsPath, "etc", "resolv.conf")
	os.MkdirAll(filepath.Dir(dnsConf), 0755)
	dnsContent := ""
	for _, ns := range m.dnsServers {
		dnsContent += "nameserver " + ns + "\n"
	}
	os.WriteFile(dnsConf, []byte(dnsContent), 0644)

	for _, d := range []string{"home/user", "tmp", "dev", "proc"} {
		os.MkdirAll(filepath.Join(m.sharedRootfsPath, d), 0755)
	}

	if len(m.preinstallPkgs) > 0 {
		args := []string{"ip", "netns", "exec", "kite-sandbox", "bwrap",
			"--bind", m.sharedRootfsPath, "/",
			"--dev", "/dev",
			"--proc", "/proc",
			"--tmpfs", "/tmp",
			"--dir", "/etc/apk",
			"--", "/sbin/apk", "add", "--no-cache",
		}
		args = append(args, m.preinstallPkgs...)
		installCmd := exec.Command(args[0], args[1:]...)
		out, err := installCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("apk add failed: %w: %s", err, string(out))
		}
	}

	m.rootfsReady = true
	return nil
}

func (m *Manager) InitUser(userID string) error {
	return os.MkdirAll(m.HomeDir(userID), 0700)
}

func (m *Manager) Exec(ctx context.Context, userID, command string) (*ExecResult, error) {
	if err := m.InitUser(userID); err != nil {
		return nil, err
	}
	home := m.HomeDir(userID)

	if m.maxDiskMB > 0 {
		size, _ := dirSize(home)
		maxBytes := int64(m.maxDiskMB) * 1024 * 1024
		if size > maxBytes {
			return &ExecResult{ExitCode: -1, Stderr: fmt.Sprintf("Sandbox disk quota exceeded (%d MB / %d MB)", size/1024/1024, maxBytes/1024/1024)}, nil
		}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.commandTimeoutSec)*time.Second)
	defer cancel()

	wrapper := fmt.Sprintf(
		"out=$(/bin/sh -c '%s' 2>&1); rc=$?; printf '%%s' \"$out\" > %s; printf '%%s' \"$out\"; exit $rc",
		escapeShell(command), "/home/user/.last_output",
	)
	_ = home

	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", "kite-sandbox", "bwrap",
		"--ro-bind", m.sharedRootfsPath, "/",
		"--bind", home, "/home/user",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--unshare-pid",
		"--die-with-parent",
		"--new-session",
		"--", "/bin/sh", "-c", wrapper,
	)
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=/home/user",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"USER=user",
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	output, runErr := cmd.CombinedOutput()
	result := &ExecResult{ExitCode: 0}

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = -1
			result.Signal = "SIGKILL (timeout)"
		} else {
			result.ExitCode = -1
			result.Signal = runErr.Error()
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.ExitCode = -1
		result.Signal = "SIGKILL (timeout)"
	}

	lastOutput := filepath.Join(home, ".last_output")
	if len(output) > 0 {
		_ = os.WriteFile(lastOutput, output, 0644)
	}

	outputStr := string(output)
	if len(outputStr) > m.maxOutputBytes {
		result.Stdout = outputStr[:m.maxOutputBytes]
		result.Truncated = true
		truncated := len(outputStr) - m.maxOutputBytes
		result.Stdout += fmt.Sprintf(
			"\n[OUTPUT TRUNCATED: %d bytes not shown. Full output saved to /home/user/.last_output. Use sandbox_read(\".last_output\", offset=%d) to read more.]",
			truncated, m.maxOutputBytes,
		)
	} else {
		result.Stdout = outputStr
	}

	return result, nil
}

func (m *Manager) DownloadsDir(userID string) string {
	return filepath.Join(m.HomeDir(userID), "downloads")
}

func (m *Manager) DownloadAttachment(ctx context.Context, userID, url, filename string) (string, error) {
	dlDir := m.DownloadsDir(userID)
	if err := os.MkdirAll(dlDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir downloads: %w", err)
	}
	if filename == "" {
		filename = filepath.Base(url)
		if filename == "" || strings.Contains(filename, "?") {
			filename = fmt.Sprintf("file_%d", time.Now().UnixNano())
		}
	}
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "..", "_")
	localPath := filepath.Join(dlDir, filename)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed")
	}
	defer resp.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("create file failed")
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("write file failed")
	}
	return localPath, nil
}

func (m *Manager) ResetUser(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.initialized, userID)
}

func (m *Manager) CleanupDownloads(dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dlDir := filepath.Join(dataDir, entry.Name(), "sandbox", "home", "downloads")
		if _, err := os.Stat(dlDir); err == nil {
			os.RemoveAll(dlDir)
		}
	}
	return nil
}

func escapeShell(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

func dirSize(path string) (int64, error) {
	var total int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, nil
}
