package main

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

func loadContextKey() ([]byte, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return contextKeyFile(filepath.Join(dir, "tfs-web", "context.key"))
}

func contextKeyFile(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, writeErr := f.Write(key)
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("Ключ контекста должен быть обычным файлом с правами 0600")
	}
	key, err = os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("Некорректный ключ контекста: требуется 32 байта; файл не перезаписан")
	}
	return key, nil
}
