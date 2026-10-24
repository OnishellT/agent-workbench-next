package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func LockStateDir(stateDir string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join(stateDir, "workbench.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		_ = lock.Close()
		return nil, err
	}
	info, err := lock.Stat()
	if err != nil {
		return fail(err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return fail(errors.New("state lock must be a private, owned regular file without hard links"))
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("state directory is already in use: %w", err))
	}
	return lock, nil
}
