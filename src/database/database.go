package database

import (
	"log"
	"os"
	"r2proxy/src/models"

	sqlite "github.com/glebarez/sqlite" // 纯 Go SQLite 驱动，无需 CGO
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// Init 初始化数据库
func Init(dsn string) error {
	var err error

	// 生产环境完全禁用 SQL 日志
	var gormLogger logger.Interface
	if os.Getenv("GIN_MODE") == "release" {
		gormLogger = logger.Default.LogMode(logger.Silent)
	} else {
		gormLogger = logger.Default.LogMode(logger.Info)
	}

	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return err
	}

	// 自动迁移
	if err := DB.AutoMigrate(
		&models.User{},
		&models.File{},
		&models.TemporaryToken{},
		&models.ShareToken{},
		&models.DownloadTask{},
	); err != nil {
		log.Printf("数据库迁移失败: %v", err)
		return err
	}

	backfillRoles()
	backfillStoredSize()
	markOrphanTasks()

	return nil
}

// backfillRoles 将旧数据的 is_admin 同步到 role 字段。
//
// AutoMigrate 为新增的 role 列填充默认值 'user'，因此不能只处理空值，
// 需要按 is_admin 双向纠正，保证两个字段始终一致。
func backfillRoles() {
	// 旧管理员：is_admin = true 但 role 不是 admin
	if err := DB.Model(&models.User{}).
		Where("is_admin = ? AND (role IS NULL OR role <> ?)", true, models.RoleAdmin).
		UpdateColumn("role", models.RoleAdmin).Error; err != nil {
		log.Printf("同步管理员角色失败: %v", err)
	}
	// 普通用户：is_admin = false 但 role 仍是 admin（例如被降权）
	if err := DB.Model(&models.User{}).
		Where("is_admin = ? AND role = ?", false, models.RoleAdmin).
		UpdateColumn("role", models.RoleUser).Error; err != nil {
		log.Printf("同步普通用户角色失败: %v", err)
	}
}

// backfillStoredSize 为历史数据补齐 stored_size。
// 压缩上传功能之前的文件是原始字节直传，stored_size 必然等于 size；
// 新上传的文件在写入时已带上真实值，因此只补 0 值不会覆盖数据。
func backfillStoredSize() {
	res := DB.Model(&models.File{}).
		Where("stored_size = 0 AND size > 0 AND status = ?", models.FileStatusConfirmed).
		UpdateColumn("stored_size", gorm.Expr("size"))
	if res.Error == nil && res.RowsAffected > 0 {
		log.Printf("已为 %d 条历史文件补齐 stored_size", res.RowsAffected)
	}
}

// markOrphanTasks 服务重启后，把遗留的 downloading 任务标记为失败，避免永久卡住
func markOrphanTasks() {
	res := DB.Model(&models.DownloadTask{}).
		Where("status IN ?", []string{models.TaskStatusDownloading, models.TaskStatusPending}).
		Updates(map[string]any{
			"status": models.TaskStatusFailed,
			"error":  "服务重启，任务已中断，请重新提交",
		})
	if res.Error == nil && res.RowsAffected > 0 {
		log.Printf("已将 %d 个未完成的下载任务标记为失败", res.RowsAffected)
	}
}

// Close 关闭数据库连接
func Close() error {
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
