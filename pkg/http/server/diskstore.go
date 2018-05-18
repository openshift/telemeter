package server

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang/snappy"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type diskStore struct {
	path string
}

func NewDiskStore(path string) Store {
	return &diskStore{
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

func (s *diskStore) ReadMetrics(ctx context.Context, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error {
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
		families, err := read(f)
		if err != nil {
			return err
		}

		if err := fn(partitionKey, families); err != nil {
			return err
		}

		return filepath.SkipDir
	})
}

func (s *diskStore) WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error {
	newestTs := newestTimestamp(families)
	t := time.Unix(newestTs/1000, (newestTs%1000)*int64(time.Millisecond))
	filename := t.UTC().Format("2006-01-02T15-04-05.999Z")

	keyHash := fnvHash(partitionKey)
	segment := []string{s.path, keyHash[0:2], keyHash[2:4], partitionKey}
	dir := filepath.Join(segment...)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("unable to create directory for partition: %v", err)
	}
	path := filepath.Join(dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		if os.IsExist(err) {
			// timestamp is the same, do nothing
			return nil
		}
		// TODO: retry
		return fmt.Errorf("unable to open file for exclusive writing: %v", err)
	}
	if err := write(f, families); err != nil {
		return fmt.Errorf("unable to write metrics to %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("unable to commit metrics to disk %s: %v", path, err)
	}
	return nil
}

func newestTimestamp(families []*clientmodel.MetricFamily) int64 {
	var newest int64
	for _, family := range families {
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

func write(w io.Writer, families []*clientmodel.MetricFamily) error {
	// output the filtered set
	compress := snappy.NewWriter(w)
	encoder := expfmt.NewEncoder(compress, expfmt.FmtProtoDelim)
	for _, family := range families {
		if family == nil {
			continue
		}
		if err := encoder.Encode(family); err != nil {
			return err
		}
	}

	if err := compress.Flush(); err != nil {
		return err
	}
	return nil
}

func read(r io.Reader) ([]*clientmodel.MetricFamily, error) {
	// output the filtered set
	families := make([]*clientmodel.MetricFamily, 0, 20)
	decompress := snappy.NewReader(r)
	decoder := expfmt.NewDecoder(decompress, expfmt.FmtProtoDelim)
	for {
		var family clientmodel.MetricFamily
		if err := decoder.Decode(&family); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		families = append(families, &family)
	}
	return families, nil
}
