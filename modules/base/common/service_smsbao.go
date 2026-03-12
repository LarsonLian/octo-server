// smsbao
package common

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"

	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"
)

type SmsbaoProvider struct {
	ctx *config.Context
	log.Log
}

// NewSmsbaoProvider 创建短信服务
func NewSmsbaoProvider(ctx *config.Context) ISMSProvider {
	return &SmsbaoProvider{
		ctx: ctx,
		Log: log.NewTLog("SmsbaoProvider"),
	}
}

func (u *SmsbaoProvider) SendSMS(ctx context.Context, zone, phone string, code string) error {
	ph := phone
	if zone != "0086" {
		if len(zone) > 2 {
			ph = strings.Replace(zone, "00", "", 1) + phone
		}
	}

	//u.ctx.GetConfig().UniSMS.AccessKeyID, u.ctx.GetConfig().UniSMS.AccessKeySecret
	//u.ctx.GetConfig().UniSMS.Signature
	//u.ctx.GetConfig().UniSMS.TemplateId

	// 状态码与提示信息的映射
	statusStr := map[string]string{
		"0":  "短信发送成功",
		"-1": "参数不全",
		"-2": "服务器空间不支持, 请确认支持curl或者fsocket，联系您的空间商解决或者更换空间！",
		"30": "密码错误",
		"40": "账号不存在",
		"41": "余额不足",
		"42": "帐户已过期",
		"43": "IP地址限制",
		"50": "内容含有敏感词",
	}

	smsapi := "https://api.smsbao.com/" // 短信API
	//user := "shyuke1688"                       // 短信平台帐号
	//pass := "74cb45173e9642478c2c07f29b82850e" // 短信平台密码，未加密的原文

	user := u.ctx.GetConfig().Smsbao.Account
	pass := u.ctx.GetConfig().Smsbao.APIKey
	tpl := u.ctx.GetConfig().Smsbao.Template
	// 加密密码为MD5
	hash := md5.New()
	hash.Write([]byte(pass))
	passMd5 := hex.EncodeToString(hash.Sum(nil))

	content := strings.Replace(tpl, "{code}", code, -1) // "您好！您的验证码是: " + code + " 。五分钟内有效。注意验证码打死也不要告诉别人哦！"

	// 构建请求URL
	params := url.Values{}
	params.Set("u", user)
	params.Set("p", passMd5)
	params.Set("m", ph)
	params.Set("c", content)

	sendurl := smsapi + "sms?" + params.Encode()

	// 发送HTTP请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(sendurl)
	if err != nil {
		u.Error("HTTP请求失败", zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		u.Error("读取响应失败", zap.Error(err))
		return err
	}

	result := string(body)
	// 显示对应的状态信息
	if msg, ok := statusStr[result]; ok {
		if result != "0" {
			u.Error("短信发送失败", zap.String("code", result), zap.String("message", msg))
			return fmt.Errorf("短信发送失败，错误码: %s, 信息: %s", result, msg)
		}
	} else {
		u.Error("短信发送未知状态", zap.String("code", result))
		return fmt.Errorf("短信发送返回未知状态码: %s", result)
	}
	return nil
}
