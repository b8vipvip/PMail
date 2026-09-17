package outbound

import (
	"encoding/json"
	"errors"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Jinnrry/pmail/config"
)

const (
	ModeDirect       = "direct"
	ModeRelay        = "relay"
	ModeTencentSES   = "tencent_ses"
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"

	TencentRegionGuangzhou = "ap-guangzhou"
	TencentRegionHongKong  = "ap-hongkong"
)

type Config struct {
	Mode     string `json:"mode"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Security string `json:"security"`

	TencentSecretID    string `json:"tencent_secret_id"`
	TencentSecretKey   string `json:"tencent_secret_key"`
	TencentRegion      string `json:"tencent_region"`
	TencentFromAddress string `json:"tencent_from_address"`
	TencentTriggerType int    `json:"tencent_trigger_type"`
}

type PublicConfig struct {
	Mode        string `json:"mode"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	Security    string `json:"security"`

	TencentSecretID     string `json:"tencent_secret_id"`
	TencentSecretKeySet bool   `json:"tencent_secret_key_set"`
	TencentRegion       string `json:"tencent_region"`
	TencentFromAddress  string `json:"tencent_from_address"`
	TencentTriggerType  int    `json:"tencent_trigger_type"`
}

var mu sync.Mutex

func defaultConfig() Config {
	return Config{
		Mode:               ModeDirect,
		Port:               587,
		Security:           SecuritySTARTTLS,
		TencentRegion:      TencentRegionGuangzhou,
		TencentTriggerType: 0,
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
		Mode:                  cfg.Mode,
		Host:                  cfg.Host,
		Port:                  cfg.Port,
		Username:              cfg.Username,
		PasswordSet:           cfg.Password != "",
		Security:              cfg.Security,
		TencentSecretID:       cfg.TencentSecretID,
		TencentSecretKeySet:   cfg.TencentSecretKey != "",
		TencentRegion:         cfg.TencentRegion,
		TencentFromAddress:    cfg.TencentFromAddress,
		TencentTriggerType:    cfg.TencentTriggerType,
	}, nil
}

// Save writes outbound secrets to config/outbound.json with mode 0600.
// When keepExistingSecrets is true, empty secret fields preserve their current values.
func Save(next Config, keepExistingSecrets bool) error {
	mu.Lock()
	defer mu.Unlock()

	current, err := readUnlocked()
	if err != nil {
		return err
	}
	if keepExistingSecrets {
		if next.Password == "" {
			next.Password = current.Password
		}
		if next.TencentSecretKey == "" {
			next.TencentSecretKey = current.TencentSecretKey
		}
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
	cfg.TencentSecretID = strings.TrimSpace(cfg.TencentSecretID)
	cfg.TencentSecretKey = strings.TrimSpace(cfg.TencentSecretKey)
	cfg.TencentRegion = strings.ToLower(strings.TrimSpace(cfg.TencentRegion))
	cfg.TencentFromAddress = strings.TrimSpace(cfg.TencentFromAddress)

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
	if cfg.TencentRegion == "" {
		cfg.TencentRegion = TencentRegionGuangzhou
	}

	if cfg.Mode != ModeDirect && cfg.Mode != ModeRelay && cfg.Mode != ModeTencentSES {
		return errors.New("outbound mode must be direct, relay or tencent_ses")
	}
	if cfg.Mode == ModeDirect {
		return nil
	}
	if cfg.Mode == ModeRelay {
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

	if cfg.TencentSecretID == "" {
		return errors.New("Tencent SES SecretId is required")
	}
	if cfg.TencentSecretKey == "" {
		return errors.New("Tencent SES SecretKey is required")
	}
	if cfg.TencentRegion != TencentRegionGuangzhou && cfg.TencentRegion != TencentRegionHongKong {
		return errors.New("Tencent SES region must be ap-guangzhou or ap-hongkong")
	}
	if cfg.TencentFromAddress == "" {
		return errors.New("Tencent SES sender address is required")
	}
	parsed, err := mail.ParseAddress(cfg.TencentFromAddress)
	if err != nil || !strings.Contains(parsed.Address, "@") {
		return errors.New("Tencent SES sender address is invalid")
	}
	if cfg.TencentTriggerType != 0 && cfg.TencentTriggerType != 1 {
		return errors.New("Tencent SES trigger type must be 0 or 1")
	}
	return nil
}
