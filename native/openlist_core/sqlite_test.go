package openlistcore

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSQLiteDSNPreservesExistingParameters(t *testing.T) {
	for _, tc := range []struct{ dsn, params, want string }{
		{"core.db", "mode=rw", "core.db?mode=rw"},
		{"core.db?cache=shared", "mode=rw", "core.db?cache=shared&mode=rw"},
		{"core.db?", "mode=rw", "core.db?mode=rw"},
		{"core.db?cache=shared&", "mode=rw", "core.db?cache=shared&mode=rw"},
		{"core.db?cache=shared", "", "core.db?cache=shared"},
	} {
		if got := appendDSNParams(tc.dsn, tc.params); got != tc.want {
			t.Errorf("appendDSNParams(%q, %q) = %q, want %q", tc.dsn, tc.params, got, tc.want)
		}
	}
}

func TestEmbeddedSQLiteUsesWALAndBusyTimeout(t *testing.T) {
	c := &core{}
	if _, err := c.initialize(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.close(); err != nil {
			t.Error(err)
		}
	})
	connection := db.GetDb()
	var mode string
	if err := connection.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	for pragma, want := range map[string]int{"busy_timeout": 10000, "synchronous": 1} {
		var got int
		if err := connection.Raw("PRAGMA " + pragma).Scan(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s = %d, want %d", pragma, got, want)
		}
	}
}

type tokenStorageDriver struct {
	driver.Driver
	storage  model.Storage
	addition map[string]string
}

func (d *tokenStorageDriver) GetStorage() *model.Storage     { return &d.storage }
func (d *tokenStorageDriver) GetAddition() driver.Additional { return d.addition }

func TestRotatedTokenSaveHandlesDatabaseFailures(t *testing.T) {
	for _, tc := range []struct {
		name                string
		locked              bool
		releaseAfterFailure bool
		wantAttempts        int
		wantToken           string
	}{
		{"锁释放后保存新令牌", true, true, 2, "rotated"},
		{"持续锁定时停止重试", true, false, 5, "original"},
		{"非锁错误不重试", false, false, 1, "original"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			c := &core{}
			if _, err := c.initialize(directory, ""); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.close(); err != nil {
					t.Error(err)
				}
			})
			connection := db.GetDb()
			sqlConnection, err := connection.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlConnection.SetMaxOpenConns(1)
			// 立即返回锁错误，让测试覆盖保存重试，而不是驱动内部的等待。
			if err := connection.Exec("PRAGMA busy_timeout=0").Error; err != nil {
				t.Fatal(err)
			}
			storage := model.Storage{MountPath: "/token-save", Driver: "test", Addition: `{"refresh_token":"original"}`}
			if err := db.CreateStorage(&storage); err != nil {
				t.Fatal(err)
			}
			table := connection.NamingStrategy.TableName("Storage")
			var transaction *gorm.DB
			if tc.locked {
				other, err := gorm.Open(openSQLite(filepath.Join(directory, "openlist.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				if err != nil {
					t.Fatal(err)
				}
				otherSQL, err := other.DB()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = otherSQL.Close() })
				transaction = other.Begin()
				if err := transaction.Exec(fmt.Sprintf("UPDATE %q SET addition=addition WHERE id=?", table), storage.ID).Error; err != nil {
					t.Fatal(err)
				}
				defer func() {
					if transaction != nil {
						_ = transaction.Rollback().Error
					}
				}()
			} else {
				if err := connection.Exec(fmt.Sprintf("CREATE TRIGGER reject_storage_save BEFORE UPDATE ON %q BEGIN SELECT RAISE(ABORT, 'storage write rejected'); END", table)).Error; err != nil {
					t.Fatal(err)
				}
			}
			attempts := 0
			if err := connection.Callback().Update().After("gorm:update").Register("test:storage-save", func(result *gorm.DB) {
				attempts++
				if tc.releaseAfterFailure && result.Error != nil && transaction != nil {
					if err := transaction.Rollback().Error; err != nil {
						t.Error(err)
					}
					transaction = nil
				}
			}); err != nil {
				t.Fatal(err)
			}
			op.MustSaveDriverStorage(&tokenStorageDriver{storage: storage, addition: map[string]string{"refresh_token": "rotated"}})
			if attempts != tc.wantAttempts {
				t.Errorf("save attempts = %d, want %d", attempts, tc.wantAttempts)
			}
			if transaction != nil {
				if err := transaction.Rollback().Error; err != nil {
					t.Fatal(err)
				}
				transaction = nil
			}
			persisted, err := db.GetStorageById(storage.ID)
			if err != nil {
				t.Fatal(err)
			}
			var addition map[string]string
			if err := json.Unmarshal([]byte(persisted.Addition), &addition); err != nil {
				t.Fatal(err)
			}
			if addition["refresh_token"] != tc.wantToken {
				t.Errorf("persisted refresh token = %q, want %q", addition["refresh_token"], tc.wantToken)
			}
		})
	}
}
