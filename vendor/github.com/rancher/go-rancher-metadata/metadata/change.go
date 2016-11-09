package metadata

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
)

func (m *client) OnChange(intervalSeconds int, do func(string)) {
	interval := time.Duration(intervalSeconds)
	version := "init"

	for {
		newVersion, err := m.waitVersion(intervalSeconds, version)
		if err != nil {
			logrus.Errorf("Error reading metadata version: %v", err)
			time.Sleep(interval * time.Second)
		} else if version == newVersion {
			logrus.Debug("No changes in metadata version")
		} else {
			logrus.Debugf("Metadata Version has been changed. Old version: %s. New version: %s.", version, newVersion)
			version = newVersion
			do(newVersion)
		}
	}
}

func (m *client) waitVersion(maxWait int, version string) (string, error) {
	resp, err := m.SendRequest(fmt.Sprintf("/version?wait=true&value=%s&maxWait=%d", version, maxWait))
	if err != nil {
		return "", err
	}
	err = json.Unmarshal(resp, &version)
	return version, err
}
