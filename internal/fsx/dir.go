package fsx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LocalDir checks that dir is a folder a program may be started in: a full
// path to an existing directory on a local or mapped drive. Network and
// device paths (\\server\share, \\?\..., \\.\...) are refused before the path
// is touched at all, because merely looking at one makes Windows sign in to
// whatever server it names. The errors are worded for the user.
func LocalDir(dir string) error {
	if strings.TrimSpace(dir) == "" || !filepath.IsAbs(dir) {
		return errors.New("cần đường dẫn đầy đủ tới một thư mục")
	}
	if runtime.GOOS == "windows" {
		if c := filepath.Clean(dir); strings.HasPrefix(c, `\\`) {
			return fmt.Errorf("chưa hỗ trợ thư mục mạng hoặc đường dẫn thiết bị: %s", dir)
		}
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("thư mục không tồn tại: %s", dir)
	}
	return nil
}
