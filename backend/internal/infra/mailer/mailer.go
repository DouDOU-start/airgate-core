// Package mailer 提供 SMTP 邮件发送功能。
package mailer

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"

	"github.com/DouDOU-start/airgate-core/internal/infra/store"
)

// Config SMTP 配置。
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	FromAddr string
	FromName string
	UseTLS   bool
}

// Mailer SMTP 邮件发送器。
type Mailer struct {
	cfg Config
}

// New 创建邮件发送器。
func New(cfg Config) *Mailer {
	return &Mailer{cfg: cfg}
}

// Send 发送邮件。
func (m *Mailer) Send(to, subject, body string) error {
	if m.cfg.Host == "" {
		slog.Warn("mail_disabled_no_config")
		return fmt.Errorf("SMTP 未配置")
	}

	from := m.cfg.FromAddr
	if m.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", m.cfg.FromName, m.cfg.FromAddr)
	}

	msg := strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	toHash := store.EmailHash(to)

	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}

	var err error
	if m.cfg.UseTLS {
		err = m.sendTLS(addr, auth, to, []byte(msg))
	} else {
		err = smtp.SendMail(addr, auth, m.cfg.FromAddr, []string{to}, []byte(msg))
	}
	if err != nil {
		slog.Error("mail_send_failed",
			"to_hash", toHash,
			"subject", subject,
			"host", m.cfg.Host,
			"port", m.cfg.Port,
			"use_tls", m.cfg.UseTLS,
			logx.LogFieldError, err)
		return err
	}
	slog.Info("mail_sent",
		"to_hash", toHash,
		"subject", subject,
		"host", m.cfg.Host)
	return nil
}

func (m *Mailer) sendTLS(addr string, auth smtp.Auth, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.cfg.Host})
	if err != nil {
		slog.Error("smtp_connect_failed", "host", m.cfg.Host, "port", m.cfg.Port, logx.LogFieldError, err)
		return fmt.Errorf("TLS dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		slog.Error("smtp_connect_failed", "host", m.cfg.Host, "port", m.cfg.Port, logx.LogFieldError, err)
		return fmt.Errorf("SMTP client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			slog.Error("smtp_auth_failed", "host", m.cfg.Host, logx.LogFieldError, err)
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := client.Mail(m.cfg.FromAddr); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}
