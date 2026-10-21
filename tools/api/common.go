package api

import (
	"encoding/json"
	"os"
	"strconv"
)

func writeJSONAtomic(targetPath string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	tmp := targetPath + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, targetPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
