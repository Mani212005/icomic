package domain

import (
	"fmt"
	"strconv"
)

// ImageQuotaLimits is an opt-in startup setting for an isolated E2E service.
// It changes enforcement limits, never persisted policy or user usage rows.
type ImageQuotaLimits struct {
	Daily   int
	Monthly int
}

func E2EImageQuota(getenv func(string) string) (*ImageQuotaLimits, error) {
	flag := getenv("ICOMIC_E2E_ISOLATED_STACK")
	daily, monthly := getenv("ICOMIC_E2E_DAILY_IMAGES"), getenv("ICOMIC_E2E_MONTHLY_IMAGES")
	if flag == "" && daily == "" && monthly == "" {
		return nil, nil
	}
	if flag != "1" {
		return nil, fmt.Errorf("E2E image limits require an isolated E2E stack")
	}
	d, errD := strconv.Atoi(daily)
	m, errM := strconv.Atoi(monthly)
	if errD != nil || errM != nil || d <= 0 || m < d {
		return nil, fmt.Errorf("E2E image limits require positive integers and monthly >= daily")
	}
	return &ImageQuotaLimits{Daily: d, Monthly: m}, nil
}
