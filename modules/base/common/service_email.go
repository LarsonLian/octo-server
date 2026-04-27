package common

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"strconv"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"go.uber.org/zap"
)

const CacheKeyEmailCode = "emailcode:"

// IEmailService 邮件服务接口
type IEmailService interface {
	// 发送验证码
	SendVerifyCode(ctx context.Context, email string, codeType CodeType) error
	// 验证验证码(销毁缓存)
	Verify(ctx context.Context, email, code string, codeType CodeType) error
	// SendHTMLEmail 发送一封 HTML 邮件（不走频率限制 / 验证码缓存，由调用方自己控制）
	SendHTMLEmail(ctx context.Context, to, subject, htmlBody string) error
}

// EmailService 邮件服务
type EmailService struct {
	ctx *config.Context
	log.Log
}

// NewEmailService 创建邮件服务
func NewEmailService(ctx *config.Context) *EmailService {
	return &EmailService{
		ctx: ctx,
		Log: log.NewTLog("EmailService"),
	}
}

// SendVerifyCode 发送验证码
func (s *EmailService) SendVerifyCode(ctx context.Context, email string, codeType CodeType) error {
	// 检查发送频率限制
	rateLimitKey := fmt.Sprintf("email_rate_limit:%s", email)
	exists, err := s.ctx.GetRedisConn().GetString(rateLimitKey)
	if err != nil {
		return err
	}
	if exists != "" {
		return errors.New("发送过于频繁，请1分钟后再试")
	}

	// 生成6位验证码
	code, err := generateSecureVerifyCode(6)
	if err != nil {
		s.Error("生成验证码失败", zap.Error(err))
		return errors.New("系统错误，请稍后重试")
	}
	s.Info("发送邮箱验证码", zap.String("email", email))

	cacheKey := fmt.Sprintf("%s%d@%s", CacheKeyEmailCode, codeType, email)
	err = s.ctx.GetRedisConn().SetAndExpire(cacheKey, code, time.Minute*5)
	if err != nil {
		return err
	}

	// 设置发送频率限制（1分钟）
	err = s.ctx.GetRedisConn().SetAndExpire(rateLimitKey, "1", time.Minute)
	if err != nil {
		return err
	}

	subject := "DMWork 验证码"
	body := fmt.Sprintf(`<div style="max-width:400px;margin:20px auto;font-family:Arial,sans-serif;padding:20px;border:1px solid #e0e0e0;border-radius:8px;">
<h2 style="color:#7c3aed;margin:0 0 16px;">DMWork</h2>
<p style="color:#333;">您的验证码为：</p>
<div style="background:#f5f3ff;padding:16px;border-radius:6px;text-align:center;margin:12px 0;">
<span style="font-size:32px;font-weight:bold;letter-spacing:8px;color:#7c3aed;">%s</span>
</div>
<p style="color:#666;font-size:13px;">验证码 5 分钟内有效，请勿泄露给他人。</p>
</div>`, code)
	return s.sendEmail(email, subject, body)
}

// SendHTMLEmail 直接发送一封 HTML 邮件。subject/body 由调用方负责，本方法
// 不写 Redis、不限速；速率控制由调用方根据业务场景自行处理。
func (s *EmailService) SendHTMLEmail(_ context.Context, to, subject, htmlBody string) error {
	if to == "" {
		return errors.New("收件人不能为空")
	}
	return s.sendEmail(to, subject, htmlBody)
}

// sendEmail 通过SMTP发送邮件（支持SSL端口465和STARTTLS端口587）
func (s *EmailService) sendEmail(to, subject, body string) error {
	cfg := s.ctx.GetConfig()
	smtpAddr := cfg.Support.EmailSmtp
	from := cfg.Support.Email
	pwd := cfg.Support.EmailPwd

	if smtpAddr == "" || from == "" || pwd == "" {
		return errors.New("邮件服务未配置，请联系管理员")
	}

	host, port, err := net.SplitHostPort(smtpAddr)
	if err != nil {
		return fmt.Errorf("smtp地址格式错误: %w", err)
	}

	auth := smtp.PlainAuth("", from, pwd, host)

	// Sanitize header fields to prevent CRLF injection.
	// An attacker could inject "Bcc: hacker@evil.com" via \r\n in to/subject.
	sanitize := func(s string) string {
		s = strings.ReplaceAll(s, "\r", "")
		s = strings.ReplaceAll(s, "\n", "")
		return s
	}
	to = sanitize(to)
	subject = sanitize(subject)
	from = sanitize(from)

	msg := "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"\r\n" +
		body + "\r\n"

	// 端口465使用直连SSL/TLS
	if port == "465" {
		tlsConfig := &tls.Config{ServerName: host}
		dialer := &net.Dialer{Timeout: smtpDialTimeout}
		conn, err := tls.DialWithDialer(dialer, "tcp", smtpAddr, tlsConfig)
		if err != nil {
			return fmt.Errorf("TLS连接失败: %w", err)
		}
		defer conn.Close()
		// 防止 SMTP 握手 / 投递阶段无限挂起 —— 异步触发的发送任务一旦泄漏会持续占用 goroutine。
		_ = conn.SetDeadline(time.Now().Add(smtpIOTimeout))

		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("创建SMTP客户端失败: %w", err)
		}
		defer client.Close()

		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP认证失败: %w", err)
		}
		if err = client.Mail(from); err != nil {
			return err
		}
		if err = client.Rcpt(to); err != nil {
			return err
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(msg))
		if err != nil {
			return err
		}
		return w.Close()
	}

	// 端口25/587使用STARTTLS。手动 dial 以注入超时（smtp.SendMail 默认无超时）。
	conn, err := net.DialTimeout("tcp", smtpAddr, smtpDialTimeout)
	if err != nil {
		return fmt.Errorf("SMTP 连接失败: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(smtpIOTimeout))

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("创建SMTP客户端失败: %w", err)
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err = client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("STARTTLS 失败: %w", err)
		}
	}
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP认证失败: %w", err)
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write([]byte(msg)); err != nil {
		return err
	}
	return w.Close()
}

const (
	smtpDialTimeout = 15 * time.Second
	smtpIOTimeout   = 60 * time.Second
)

// Verify 验证验证码（验证成功后销毁缓存）
func (s *EmailService) Verify(ctx context.Context, email, code string, codeType CodeType) error {
	// 检查是否被锁定
	lockKey := fmt.Sprintf("email_verify_lock:%s", email)
	locked, err := s.ctx.GetRedisConn().GetString(lockKey)
	if err != nil {
		return err
	}
	if locked != "" {
		return errors.New("验证失败次数过多，请10分钟后再试")
	}

	// 支持测试验证码（仅限非 release 模式；release 下即便配置了 SMSCode 也不会匹配）
	if MatchTestCode(s.ctx.GetConfig(), code) {
		log.Warn("email verify passed via test SMSCode", zap.String("email", maskEmail(email)))
		return nil
	}

	cacheKey := fmt.Sprintf("%s%d@%s", CacheKeyEmailCode, codeType, email)
	sysCode, err := s.ctx.GetRedisConn().GetString(cacheKey)
	if err != nil {
		return err
	}
	if sysCode != "" && subtle.ConstantTimeCompare([]byte(sysCode), []byte(code)) == 1 {
		s.ctx.GetRedisConn().Del(cacheKey)
		// 验证成功，清除失败计数
		failCountKey := fmt.Sprintf("email_verify_fail:%s", email)
		s.ctx.GetRedisConn().Del(failCountKey)
		s.ctx.GetRedisConn().Del(lockKey)
		return nil
	}

	// 验证失败，增加失败计数
	failCountKey := fmt.Sprintf("email_verify_fail:%s", email)
	failCountStr, _ := s.ctx.GetRedisConn().GetString(failCountKey)
	failCount := 0
	if failCountStr != "" {
		if count, err := strconv.Atoi(failCountStr); err == nil {
			failCount = count
		}
	}
	failCount++

	if failCount >= 3 {
		s.ctx.GetRedisConn().SetAndExpire(lockKey, "1", time.Minute*10)
		return errors.New("验证失败次数过多，已锁定10分钟")
	}
	s.ctx.GetRedisConn().SetAndExpire(failCountKey, fmt.Sprintf("%d", failCount), time.Minute*10)

	s.Info("邮箱验证码错误", zap.String("email", email))
	return errors.New("验证码无效！")
}
