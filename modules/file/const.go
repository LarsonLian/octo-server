package file

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Type 文件类型
type Type string

const (
	// TypeChat 聊天文件
	TypeChat Type = "chat"
	// TypeMoment 动态文件
	TypeMoment Type = "moment"
	// TypeMomentCover 动态封面
	TypeMomentCover Type = "momentcover"
	// TypeSticker 表情
	TypeSticker Type = "sticker"
	// TypeReport 举报
	TypeReport Type = "report"
	// TypeCommon 通用
	TypeCommon Type = "common"
	// TypeChatBg 聊天背景
	TypeChatBg Type = "chatbg"
	// TypeDownload 下载文件目录
	TypeDownload = "download"
	// TypeWorkplaceBanner
	TypeWorkplaceBanner Type = "workplacebanner"
	// TypeWorkplaceAppIcon
	TypeWorkplaceAppIcon Type = "workplaceappicon"
)

// MaxFileSize 最大文件大小（100MB）
const MaxFileSize int64 = 100 * 1024 * 1024

// allowedExtensions 允许上传的文件扩展名
var allowedExtensions = map[string]bool{
	// 图片
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".webp": true, ".ico": true,
	// 文档
	".pdf": true, ".doc": true, ".docx": true, ".xls": true,
	".xlsx": true, ".ppt": true, ".pptx": true, ".txt": true,
	".csv": true, ".rtf": true, ".odt": true, ".ods": true,
	".md": true, ".html": true, ".htm": true,
	// 音频
	".mp3": true, ".wav": true, ".aac": true, ".flac": true,
	".ogg": true, ".wma": true, ".m4a": true, ".amr": true,
	// 视频
	".mp4": true, ".avi": true, ".mov": true, ".wmv": true,
	".flv": true, ".mkv": true, ".webm": true, ".m4v": true,
	// 压缩包
	".zip": true, ".rar": true, ".7z": true, ".tar": true,
	".gz": true, ".bz2": true, ".xz": true,
	// 其他
	".json": true, ".xml": true, ".yaml": true, ".yml": true,
}

// blockedExtensions 禁止上传的文件扩展名（可执行文件）
var blockedExtensions = map[string]bool{
	".exe": true, ".bat": true, ".sh": true, ".cmd": true,
	".msi": true, ".dll": true, ".com": true, ".scr": true,
	".pif": true, ".vbs": true, ".vbe": true, ".js": true,
	".jse": true, ".wsf": true, ".wsh": true, ".ps1": true,
	".sys": true, ".cpl": true, ".inf": true, ".reg": true,
	".apk": true, ".ipa": true,
	".php": true, ".jsp": true, ".asp": true, ".aspx": true,
	".cgi": true, ".py": true, ".rb": true, ".pl": true,
}

// IsAllowedExtension 检查文件扩展名是否允许上传
func IsAllowedExtension(ext string) bool {
	ext = strings.ToLower(ext)
	if blockedExtensions[ext] {
		return false
	}
	return allowedExtensions[ext]
}

// IsBlockedExtension 检查文件扩展名是否被禁止
func IsBlockedExtension(ext string) bool {
	return blockedExtensions[strings.ToLower(ext)]
}

// sanitizeFilename 清洗文件名，去除路径分隔符、CRLF、控制字符等危险字符
func sanitizeFilename(name string) string {
	// 去除路径前缀，只保留文件名部分
	name = filepath.Base(name)
	// 替换 Windows 路径分隔符残留
	name = strings.ReplaceAll(name, "\\", "_")

	// 过滤危险字符：CRLF、控制字符、双引号
	var b strings.Builder
	for _, r := range name {
		if r == '\r' || r == '\n' || r == '"' || r == '\x00' {
			b.WriteRune('_')
		} else if r < 0x20 { // 其他控制字符
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	name = b.String()

	// 限制长度为 255 字符（按 UTF-8 字符数）
	if utf8.RuneCountInString(name) > 255 {
		runes := []rune(name)
		ext := filepath.Ext(name)
		extRunes := []rune(ext)
		// 保留扩展名，截断文件名主体
		if len(extRunes) < 255 {
			name = string(runes[:255-len(extRunes)]) + ext
		} else {
			name = string(runes[:255])
		}
	}

	if name == "" || name == "." {
		name = "file"
	}

	return name
}
