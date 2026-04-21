package category

import (
	"os"
	"sync"
)

const (
	defaultCategoryNamePlaceholder = "__default__"
	defaultCategoryNameFallback    = "默认分组"
)

var (
	_defaultCategoryName     string
	_defaultCategoryNameOnce sync.Once
)

func defaultCategoryName() string {
	_defaultCategoryNameOnce.Do(func() {
		if v := os.Getenv("DM_DEFAULT_CATEGORY_NAME"); v != "" {
			_defaultCategoryName = v
		} else {
			_defaultCategoryName = defaultCategoryNameFallback
		}
	})
	return _defaultCategoryName
}
