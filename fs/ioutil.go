package fs

import (
	"context"
	"encoding/json"
	"os"
	pathpkg "path"

	"github.com/shurcooL/webdavfs/vfsutil"
	"golang.org/x/net/webdav"
)

// jsonEncodeFile encodes v into file at path, overwriting or creating it.
// The parent directory must exist, otherwise an error will be returned.
func jsonEncodeFile(ctx context.Context, fs webdav.FileSystem, path string, v interface{}) error {
	f, err := fs.OpenFile(ctx, path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(v)
}

// jsonEncodeFileWithMkdirAll encodes v into file at path, overwriting or creating it.
// The parent directory is created if it doesn't exist.
func jsonEncodeFileWithMkdirAll(ctx context.Context, fs webdav.FileSystem, path string, v interface{}) error {
	f, openError := fs.OpenFile(ctx, path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if os.IsNotExist(openError) {
		// The parent directory may not exist. Create it, and try again.
		err := vfsutil.MkdirAll(ctx, fs, pathpkg.Dir(path), 0700)
		if err != nil {
			return err
		}
		f, openError = fs.OpenFile(ctx, path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	}
	if openError != nil {
		return openError
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(v)
}

// jsonDecodeFile decodes contents of file at path into v.
func jsonDecodeFile(ctx context.Context, fs webdav.FileSystem, path string, v interface{}) error {
	f, err := vfsutil.Open(ctx, fs, path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}
