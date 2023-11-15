package flag

import (
	"flag"
	"fmt"

	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"github.com/robfig/cron/v3"
	"k8s.io/utils/ptr"
)

type CronSchedule images.CronSchedule

func (v *CronSchedule) String() string {
	return string(*v)
}

func (v *CronSchedule) Get() images.CronSchedule {
	return images.CronSchedule(*v)
}

func (v *CronSchedule) Set(value string) error {
	_, err := cron.ParseStandard(value)
	if err != nil {
		return fmt.Errorf("failed to parse cron schedule: %w", err)
	}
	*v = CronSchedule(value)
	return nil
}

var _ flag.Value = ptr.To(CronSchedule(""))
