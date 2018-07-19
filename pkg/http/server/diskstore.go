package server

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	clientmodel "github.com/prometheus/client_model/go"

	"github.com/openshift/telemeter/pkg/metricsclient"
)

type DiskStore struct {
	path string
}

func NewDiskStore(path string) Store {
	return &DiskStore{
		path: path,
	}
}

func lastFile(files []os.FileInfo) os.FileInfo {
	for i := len(files) - 1; i >= 0; i-- {
		if files[i].IsDir() {
			continue
		}
		return files[i]
	}
	return nil
}

func (s *DiskStore) ReadMetrics(ctx context.Context, minTimestampMs int64, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error {
	return filepath.Walk(s.path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// directory structure is hash[0-2]/hash[3-4]/partitionKey
		if info.IsDir() {
			dir := strings.TrimPrefix(strings.TrimPrefix(path, s.path), string(filepath.Separator))
			if strings.Count(dir, string(filepath.Separator)) != 2 {
				return nil
			}
		} else {
			return nil
		}

		partitionKey := info.Name()

		// find the newest (lexographic highest) file in the directory
		files, err := ioutil.ReadDir(path)
		if err != nil {
			return err
		}
		lastFile := lastFile(files)
		if lastFile == nil {
			return fn(partitionKey, nil)
		}
		originalPath := path
		path = filepath.Join(path, lastFile.Name())
		go func() {
			for _, file := range files {
				if file == lastFile || file.IsDir() {
					continue
				}
				// clean up the old files
				os.Remove(filepath.Join(originalPath, file.Name()))
			}
		}()

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		families, err := metricsclient.Read(f)
		if err != nil {
			return fmt.Errorf("unable to read data for %s from %s: %v", partitionKey, lastFile.Name(), err)
		}

		if minTimestampMs > 0 && minTimestampMs > newestTimestamp(families) {
			f.Close()
			os.Remove(path)
			return nil
		}

		if err := fn(partitionKey, families); err != nil {
			return err
		}

		return filepath.SkipDir
	})
}

func (s *DiskStore) WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error {
	storageKey := filenameForFamilies(families)

	path, err := pathForPartitionAndStorageKey(s.path, partitionKey, storageKey)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		if os.IsExist(err) {
			// timestamp is the same, do nothing
			return nil
		}
		// TODO: retry
		return fmt.Errorf("unable to open file for exclusive writing: %v", err)
	}
	if err := metricsclient.Write(f, families); err != nil {
		return fmt.Errorf("unable to write metrics to %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("unable to commit metrics to disk %s: %v", path, err)
	}
	return nil
}

func pathForPartitionAndStorageKey(base, partitionKey, storageKey string) (string, error) {
	keyHash := fnvHash(partitionKey)
	segment := []string{base, keyHash[0:2], keyHash[2:4], partitionKey}
	dir := filepath.Join(segment...)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("unable to create directory for partition: %v", err)
	}
	return filepath.Join(dir, storageKey), nil
}

func filenameForFamilies(families []*clientmodel.MetricFamily) string {
	newestTs := newestTimestamp(families)
	t := time.Unix(newestTs/1000, (newestTs%1000)*int64(time.Millisecond))
	filename := t.UTC().Format("2006-01-02T15-04-05.999Z")
	return filename
}

func newestTimestamp(families []*clientmodel.MetricFamily) int64 {
	var newest int64
	for _, family := range families {
		if len(family.Metric) == 0 {
			continue
		}
		t := *family.Metric[len(family.Metric)-1].TimestampMs
		if t > newest {
			newest = t
		}
	}
	return newest
}

func fnvHash(text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return strconv.FormatUint(h.Sum64(), 32)
}
