package bootstrap

import (
	"context"

	"github.com/PeterChen1997/synctv/internal/vendor"
)

func InitVendorBackend(ctx context.Context) error {
	return vendor.Init(ctx)
}
