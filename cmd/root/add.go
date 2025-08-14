package root

import (
	"errors"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/PeterChen1997/synctv/internal/bootstrap"
	"github.com/PeterChen1997/synctv/internal/db"
)

var AddCmd = &cobra.Command{
	Use:   "add",
	Short: "add root by user id",
	Long:  `add root by user id`,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
		return bootstrap.New().Add(
			bootstrap.InitStdLog,
			bootstrap.InitConfig,
			bootstrap.InitDatabase,
		).Run(cmd.Context())
	},
	RunE: func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return errors.New("missing user id")
		}
		u, err := db.GetUserByID(args[0])
		if err != nil {
			log.Errorf("get user failed: %s", err)
			return nil
		}
		if err := db.AddRoot(u); err != nil {
			log.Errorf("add root failed: %s", err)
			return nil
		}
		log.Infof("add root success: %s\n", u.Username)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(AddCmd)
}
