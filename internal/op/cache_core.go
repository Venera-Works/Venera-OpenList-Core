//go:build venera_core

package op

import (
	"github.com/OpenListTeam/OpenList/v4/internal/cache"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func newUserCache() *cache.KeyedCache[*model.User] { return nil }
