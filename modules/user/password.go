package user

import (
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 10

// HashPassword 使用 bcrypt 哈希密码
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword 验证密码是否匹配存储的哈希值
// 同时支持 bcrypt 和旧版 MD5(MD5(password)) 格式
// 返回: matched（是否匹配）, needsMigration（是否需要迁移到 bcrypt）
func CheckPassword(password, storedHash string) (matched bool, needsMigration bool) {
	if isBcryptHash(storedHash) {
		err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
		return err == nil, false
	}
	// 旧版 MD5(MD5(password)) 验证
	md5Hash := util.MD5(util.MD5(password))
	if md5Hash == storedHash {
		return true, true
	}
	return false, false
}

// isBcryptHash 判断存储的哈希值是否为 bcrypt 格式
func isBcryptHash(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") ||
		strings.HasPrefix(hash, "$2b$") ||
		strings.HasPrefix(hash, "$2y$")
}
