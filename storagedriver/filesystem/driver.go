package filesystem

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"time"

	"github.com/docker/docker-registry/storagedriver"
	"github.com/docker/docker-registry/storagedriver/factory"
)

const driverName = "filesystem"
const defaultRootDirectory = "/tmp/registry/storage"

func init() {
	factory.Register(driverName, &filesystemDriverFactory{})
}

// filesystemDriverFactory implements the factory.StorageDriverFactory interface
type filesystemDriverFactory struct{}

func (factory *filesystemDriverFactory) Create(parameters map[string]interface{}) (storagedriver.StorageDriver, error) {
	return FromParameters(parameters), nil
}

// Driver is a storagedriver.StorageDriver implementation backed by a local
// filesystem. All provided paths will be subpaths of the RootDirectory
type Driver struct {
	rootDirectory string
}

// FromParameters constructs a new Driver with a given parameters map
// Optional Parameters:
// - rootdirectory
func FromParameters(parameters map[string]interface{}) *Driver {
	var rootDirectory = defaultRootDirectory
	if parameters != nil {
		rootDir, ok := parameters["rootdirectory"]
		if ok {
			rootDirectory = fmt.Sprint(rootDir)
		}
	}
	return New(rootDirectory)
}

// New constructs a new Driver with a given rootDirectory
func New(rootDirectory string) *Driver {
	return &Driver{rootDirectory}
}

// Implement the storagedriver.StorageDriver interface

// GetContent retrieves the content stored at "path" as a []byte.
func (d *Driver) GetContent(path string) ([]byte, error) {
	if !storagedriver.PathRegexp.MatchString(path) {
		return nil, storagedriver.InvalidPathError{Path: path}
	}

	rc, err := d.ReadStream(path, 0)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	p, err := ioutil.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// PutContent stores the []byte content at a location designated by "path".
func (d *Driver) PutContent(subPath string, contents []byte) error {
	if !storagedriver.PathRegexp.MatchString(subPath) {
		return storagedriver.InvalidPathError{Path: subPath}
	}

	if _, err := d.WriteStream(subPath, 0, bytes.NewReader(contents)); err != nil {
		return err
	}

	return os.Truncate(d.fullPath(subPath), int64(len(contents)))
}

// ReadStream retrieves an io.ReadCloser for the content stored at "path" with a
// given byte offset.
func (d *Driver) ReadStream(path string, offset int64) (io.ReadCloser, error) {
	if !storagedriver.PathRegexp.MatchString(path) {
		return nil, storagedriver.InvalidPathError{Path: path}
	}

	if offset < 0 {
		return nil, storagedriver.InvalidOffsetError{Path: path, Offset: offset}
	}

	file, err := os.OpenFile(d.fullPath(path), os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storagedriver.PathNotFoundError{Path: path}
		}

		return nil, err
	}

	seekPos, err := file.Seek(int64(offset), os.SEEK_SET)
	if err != nil {
		file.Close()
		return nil, err
	} else if seekPos < int64(offset) {
		file.Close()
		return nil, storagedriver.InvalidOffsetError{Path: path, Offset: offset}
	}

	return file, nil
}

// WriteStream stores the contents of the provided io.Reader at a location
// designated by the given path.
func (d *Driver) WriteStream(subPath string, offset int64, reader io.Reader) (nn int64, err error) {
	if !storagedriver.PathRegexp.MatchString(subPath) {
		return 0, storagedriver.InvalidPathError{Path: subPath}
	}

	if offset < 0 {
		return 0, storagedriver.InvalidOffsetError{Path: subPath, Offset: offset}
	}

	// TODO(stevvooe): This needs to be a requirement.
	// if !path.IsAbs(subPath) {
	// 	return fmt.Errorf("absolute path required: %q", subPath)
	// }

	fullPath := d.fullPath(subPath)
	parentDir := path.Dir(fullPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return 0, err
	}

	fp, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		// TODO(stevvooe): A few missing conditions in storage driver:
		//	1. What if the path is already a directory?
		//  2. Should number 1 be exposed explicitly in storagedriver?
		//	2. Can this path not exist, even if we create above?
		return 0, err
	}
	defer fp.Close()

	nn, err = fp.Seek(offset, os.SEEK_SET)
	if err != nil {
		return 0, err
	}

	if nn != offset {
		return 0, fmt.Errorf("bad seek to %v, expected %v in fp=%v", offset, nn, fp)
	}

	return io.Copy(fp, reader)
}

// Stat retrieves the FileInfo for the given path, including the current size
// in bytes and the creation time.
func (d *Driver) Stat(subPath string) (storagedriver.FileInfo, error) {
	if !storagedriver.PathRegexp.MatchString(subPath) {
		return nil, storagedriver.InvalidPathError{Path: subPath}
	}

	fullPath := d.fullPath(subPath)

	fi, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storagedriver.PathNotFoundError{Path: subPath}
		}

		return nil, err
	}

	return fileInfo{
		path:     subPath,
		FileInfo: fi,
	}, nil
}

// List returns a list of the objects that are direct descendants of the given
// path.
func (d *Driver) List(subPath string) ([]string, error) {
	if !storagedriver.PathRegexp.MatchString(subPath) && subPath != "/" {
		return nil, storagedriver.InvalidPathError{Path: subPath}
	}

	if subPath[len(subPath)-1] != '/' {
		subPath += "/"
	}
	fullPath := d.fullPath(subPath)

	dir, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storagedriver.PathNotFoundError{Path: subPath}
		}
		return nil, err
	}

	defer dir.Close()

	fileNames, err := dir.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(fileNames))
	for _, fileName := range fileNames {
		keys = append(keys, path.Join(subPath, fileName))
	}

	return keys, nil
}

// Move moves an object stored at sourcePath to destPath, removing the original
// object.
func (d *Driver) Move(sourcePath string, destPath string) error {
	if !storagedriver.PathRegexp.MatchString(sourcePath) {
		return storagedriver.InvalidPathError{Path: sourcePath}
	} else if !storagedriver.PathRegexp.MatchString(destPath) {
		return storagedriver.InvalidPathError{Path: destPath}
	}

	source := d.fullPath(sourcePath)
	dest := d.fullPath(destPath)

	if _, err := os.Stat(source); os.IsNotExist(err) {
		return storagedriver.PathNotFoundError{Path: sourcePath}
	}

	if err := os.MkdirAll(path.Dir(dest), 0755); err != nil {
		return err
	}

	err := os.Rename(source, dest)
	return err
}

// Delete recursively deletes all objects stored at "path" and its subpaths.
func (d *Driver) Delete(subPath string) error {
	if !storagedriver.PathRegexp.MatchString(subPath) {
		return storagedriver.InvalidPathError{Path: subPath}
	}

	fullPath := d.fullPath(subPath)

	_, err := os.Stat(fullPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	} else if err != nil {
		return storagedriver.PathNotFoundError{Path: subPath}
	}

	err = os.RemoveAll(fullPath)
	return err
}

// fullPath returns the absolute path of a key within the Driver's storage.
func (d *Driver) fullPath(subPath string) string {
	return path.Join(d.rootDirectory, subPath)
}

type fileInfo struct {
	os.FileInfo
	path string
}

var _ storagedriver.FileInfo = fileInfo{}

// Path provides the full path of the target of this file info.
func (fi fileInfo) Path() string {
	return fi.path
}

// Size returns current length in bytes of the file. The return value can
// be used to write to the end of the file at path. The value is
// meaningless if IsDir returns true.
func (fi fileInfo) Size() int64 {
	if fi.IsDir() {
		return 0
	}

	return fi.FileInfo.Size()
}

// ModTime returns the modification time for the file. For backends that
// don't have a modification time, the creation time should be returned.
func (fi fileInfo) ModTime() time.Time {
	return fi.FileInfo.ModTime()
}

// IsDir returns true if the path is a directory.
func (fi fileInfo) IsDir() bool {
	return fi.FileInfo.IsDir()
}
