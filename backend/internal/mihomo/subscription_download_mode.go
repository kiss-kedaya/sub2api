package mihomo

import "errors"

var ErrSubscriptionDownloadMode = errors.New("invalid subscription download mode")

type SubscriptionDownloadMode string

const (
	SubscriptionDownloadAuto   SubscriptionDownloadMode = "auto"
	SubscriptionDownloadProxy  SubscriptionDownloadMode = "proxy"
	SubscriptionDownloadDirect SubscriptionDownloadMode = "direct"
)

func normalizeSubscriptionDownloadMode(mode SubscriptionDownloadMode) (SubscriptionDownloadMode, error) {
	if mode == "" {
		return SubscriptionDownloadAuto, nil
	}
	switch mode {
	case SubscriptionDownloadAuto, SubscriptionDownloadProxy, SubscriptionDownloadDirect:
		return mode, nil
	}
	return "", ErrSubscriptionDownloadMode
}
