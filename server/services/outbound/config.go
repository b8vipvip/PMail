package outbound

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Jinnrry/pmail/config"
)

const (
	ModeDirect       = "direct"
	ModeRelay        = "relay"
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"
)

type Config struct {
	Mode     string `json:"mode"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Security string `json:"security"`
}

type PublicConfig struct {
	Mode        string `json:"mode"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	Security    string `json:"security"`
}

var mu sync.Mutex

func defaultConfig() Config {
	return Config{
		Mode:     ModeDirect,
		Port:     587,
		Security: SecuritySTARTTLS,
	}
}

func configPath() string {
	return filepath.Join(config.ROOT_PATH, "config", "outbound.json")
}

func Get() (Config, error) {
	mu.Lock()
	defer mu.Unlock()
	return readUnlocked()
}

func GetPublic() (PublicConfig, error) {
	cfg, err := Get()
	if err != nil {
		return PublicConfig{}, err
	}
	return PublicConfig{
		Mode:        cfg.Mode,
		Host:        cfg.Host,
		Port:        cfg.Port,
		Username:    cfg.Username,
		PasswordSet: cfg.Password != "",
		Security:    cfg.Security,
	}, nil
}

func Save(next Config, keepExistingPassword bool) error {
	mu.Lock()
	defer mu.Unlock()

	current, err := readUnlocked()
	if err != nil {
		return err
	}
	if keepExistingPassword && next.Password == "" {
		next.Password = current.Password
	}
	if err := normalizeAndValidate(&next); err != nil {
		return err
	}

	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0600)
}

func readUnlocked() (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(configPath())
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if err := normalizeAndValidate(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func normalizeAndValidate(cfg *Config) error {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.Security = strings.ToLower(strings.TrimSpace(cfg.Security))

	if cfg.Mode == "" {
		cfg.Mode = ModeDirect
	}
	if cfg.Security == "" {
		cfg.Security = SecuritySTARTTLS
	}
	if cfg.Port == 0 {
		if cfg.Security == SecurityTLS {
			cfg.Port = 465
		} else {
			cfg.Port = 587
		}
	}

	if cfg.Mode != ModeDirect && cfg.Mode != ModeRelay {
		return errors.New("outbound mode must be direct or relay")
	}
	if cfg.Mode == ModeDirect {
		return nil
	}
	if cfg.Host == "" {
		return errors.New("SMTP relay host is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("SMTP relay port is invalid")
	}
	if cfg.Security != SecuritySTARTTLS && cfg.Security != SecurityTLS {
		return errors.New("SMTP relay security must be starttls or tls")
	}
	return nil
}
