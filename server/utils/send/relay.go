package send

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	stdsmtp "net/smtp"
	"strings"
	"time"

	"github.com/Jinnrry/pmail/dto/parsemail"
	"github.com/Jinnrry/pmail/services/outbound"
	"github.com/Jinnrry/pmail/utils/context"
	log "github.com/sirupsen/logrus"
)

const relayIOTimeout = 30 * time.Second

// tryRelay handles delivery when authenticated SMTP relay mode is enabled.
// It returns handled=false when PMail should continue with direct-to-MX delivery.
func tryRelay(ctx *context.Context, fromDomain string, data []byte, to []*parsemail.User, from string) (handled bool, deliveryErr error, domainErrors map[string]error) {
	cfg, err := outbound.Get()
	if err != nil {
		return true, fmt.Errorf("load outbound relay configuration: %w", err), map[string]error{"relay": err}
	}
	if cfg.Mode != outbound.ModeRelay {
		return false, nil, nil
	}

	recipients := buildAddress(to)
	if len(recipients) == 0 {
		err := errors.New("SMTP relay has no recipients")
		return true, err, map[string]error{"relay": err}
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	log.WithContext(ctx).Infof("Outbound relay delivery: host=%s port=%d security=%s recipients=%d", cfg.Host, cfg.Port, cfg.Security, len(recipients))

	err = sendSMTPRelay(cfg.Host, addr, cfg.Username, cfg.Password, cfg.Security, from, recipients, data)
	domainErrors = relayDomainErrors(to, err)
	if err != nil {
		log.WithContext(ctx).Errorf("Outbound relay delivery failed: host=%s port=%d error=%v", cfg.Host, cfg.Port, err)
		return true, fmt.Errorf("SMTP relay delivery failed: %w", err), domainErrors
	}

	log.WithContext(ctx).Infof("Outbound relay accepted message: host=%s recipients=%d", cfg.Host, len(recipients))
	return true, nil, domainErrors
}

func sendSMTPRelay(host, addr, username, password, security, from string, to []string, msg []byte) error {
	var conn net.Conn
	var err error

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tlsConfig := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}

	switch security {
	case outbound.SecurityTLS:
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	case outbound.SecuritySTARTTLS:
		conn, err = dialer.Dial("tcp", addr)
	default:
		return fmt.Errorf("unsupported SMTP relay security: %s", security)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(relayIOTimeout))

	client, err := stdsmtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if security == outbound.SecuritySTARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return errors.New("SMTP relay does not advertise STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}

	if username != "" || password != "" {
		if username == "" || password == "" {
			return errors.New("SMTP relay username and password must both be configured")
		}
		auth := stdsmtp.PlainAuth("", username, password, host)
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP relay authentication failed: %w", err)
		}
	}

	if err = client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if strings.TrimSpace(recipient) == "" {
			continue
		}
		if err = client.Rcpt(recipient); err != nil {
			return fmt.Errorf("recipient %s rejected: %w", recipient, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}

	// DATA's final 250 means the relay accepted responsibility. Do not wait for
	// QUIT here; closing avoids turning a missing 221 into an accidental retry.
	return nil
}

func relayDomainErrors(to []*parsemail.User, deliveryErr error) map[string]error {
	result := map[string]error{}
	for _, recipient := range to {
		if recipient == nil {
			continue
		}
		_, domain := recipient.GetDomainAccount()
		if domain == "" {
			domain = "relay"
		}
		result[domain] = deliveryErr
	}
	return result
}

var _ io.Writer
