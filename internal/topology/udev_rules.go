package topology

import (
	"errors"
	"fmt"
)

type ChannelRule struct {
	IDPathTag string
	Symlink   string
}

func RenderUdevRule(rule ChannelRule) (string, error) {
	if rule.IDPathTag == "" {
		return "", errors.New("id path tag is required")
	}
	if rule.Symlink == "" {
		return "", errors.New("symlink is required")
	}
	return fmt.Sprintf(`SUBSYSTEM=="tty", ENV{ID_PATH_TAG}=="%s", SYMLINK+="%s"`+"\n", rule.IDPathTag, rule.Symlink), nil
}
