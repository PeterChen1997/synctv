package bootstrap

import (
	"context"

	"github.com/PeterChen1997/synctv/internal/op"
)

func InitOp(_ context.Context) error {
	return op.Init(4096)
}
