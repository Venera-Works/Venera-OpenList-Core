//go:build !venera_core

package op

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/cache"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func newUserCache() *cache.KeyedCache[*model.User] {
	return cache.NewKeyedCache[*model.User](time.Hour)
}

// cache user data
func (cm *CacheManager) SetUser(username string, user *model.User) {
	cm.userCache.Set(username, user)
}

// cached user data
func (cm *CacheManager) GetUser(username string) (*model.User, bool) {
	return cm.userCache.Get(username)
}

// remove user data from cache
func (cm *CacheManager) DeleteUser(username string) {
	cm.userCache.Delete(username)
}
