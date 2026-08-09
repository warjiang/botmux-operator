package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultHome       = "/data/home/botmux"
	defaultWorkingDir = "/workspace"
)

type Options struct {
	Home          string
	WorkingDir    string
	WorkspacePath string
	AppID         string
	AppSecretFile string
	Brand         string
	CLIID         string
	Model         string
	Backend       string
	Sandbox       bool
}

func OptionsFromEnv() Options {
	return Options{
		Home:          envOr("BOTMUX_BOOTSTRAP_HOME", defaultHome),
		WorkingDir:    envOr("BOTMUX_BOOTSTRAP_WORKING_DIR", defaultWorkingDir),
		WorkspacePath: envOr("BOTMUX_BOOTSTRAP_WORKSPACE_PATH", "/data/workspace"),
		AppID:         os.Getenv("BOTMUX_BOOTSTRAP_APP_ID"),
		AppSecretFile: os.Getenv("BOTMUX_BOOTSTRAP_APP_SECRET_FILE"),
		Brand:         envOr("BOTMUX_BOOTSTRAP_BRAND", "feishu"),
		CLIID:         os.Getenv("BOTMUX_BOOTSTRAP_CLI_ID"),
		Model:         os.Getenv("BOTMUX_BOOTSTRAP_MODEL"),
		Backend:       envOr("BOTMUX_BOOTSTRAP_BACKEND", "tmux"),
		Sandbox:       os.Getenv("BOTMUX_BOOTSTRAP_SANDBOX") == "true",
	}
}

func Run(opts Options) error {
	if opts.Home == "" || opts.WorkingDir == "" || opts.WorkspacePath == "" || opts.AppID == "" || opts.CLIID == "" {
		return errors.New("home, workingDir, workspacePath, appId and cliId are required")
	}
	if opts.AppSecretFile == "" {
		return errors.New("app secret file is required")
	}

	secret, err := os.ReadFile(opts.AppSecretFile)
	if err != nil {
		return fmt.Errorf("read app secret: %w", err)
	}
	if len(secret) == 0 {
		return errors.New("app secret is empty")
	}

	configDir := filepath.Join(opts.Home, ".botmux")
	configPath := filepath.Join(configDir, "bots.json")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create botmux config directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "data"), 0o700); err != nil {
		return fmt.Errorf("create botmux data directory: %w", err)
	}
	if err := os.MkdirAll(opts.WorkspacePath, 0o750); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}

	entry, err := readExisting(configPath)
	if err != nil {
		return err
	}
	entry["larkAppId"] = opts.AppID
	entry["larkAppSecret"] = string(secret)
	entry["brand"] = opts.Brand
	entry["cliId"] = opts.CLIID
	entry["backendType"] = opts.Backend
	entry["workingDir"] = opts.WorkingDir
	entry["sandbox"] = opts.Sandbox
	if opts.Model == "" {
		delete(entry, "model")
	} else {
		entry["model"] = opts.Model
	}

	data, err := json.MarshalIndent([]map[string]any{entry}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode botmux config: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(configDir, ".bots.json-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("replace botmux config: %w", err)
	}
	return nil
}

func readExisting(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing botmux config: %w", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse existing botmux config: %w", err)
	}
	if len(entries) == 0 || entries[0] == nil {
		return map[string]any{}, nil
	}
	return entries[0], nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
