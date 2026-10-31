package opendata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// CKANBase is the opendata.az CKAN API (the public site's backend).
const CKANBase = "https://admin.opendata.az/api/3/action"

// Dataset names on opendata.az.
const (
	DatasetRoutes    = "avtobuslarin-hereket-trayektoriyasi"
	DatasetStops     = "bakubus-mmc-ye-mexsus-avtobus-marsrutlari-uzre-dayanacaqlarin-cografi-koordinatlari"
	DatasetRidership = "bakubus-marsrutlari-uzre-gundelik-sernisin-gedislerinin-sayi-2026"
)

// Resource describes a downloadable file of a dataset, with provenance.
type Resource struct {
	DatasetName string
	Title       string
	Publisher   string
	License     string
	PageURL     string
	FileURL     string
	Modified    string
}

// Resolve looks up a dataset's first resource via the CKAN search API, so
// download URLs are never hard-coded.
func Resolve(ctx context.Context, c *http.Client, datasetName string) (Resource, error) {
	q := url.Values{"fq": {"name:" + datasetName}, "rows": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CKANBase+"/package_search?"+q.Encode(), nil)
	if err != nil {
		return Resource{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return Resource{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Resource{}, fmt.Errorf("opendata.az search: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Success bool `json:"success"`
		Result  struct {
			Results []struct {
				Name             string `json:"name"`
				Title            string `json:"title"`
				LicenseTitle     string `json:"license_title"`
				MetadataModified string `json:"metadata_modified"`
				Organization     struct {
					Title string `json:"title"`
				} `json:"organization"`
				Resources []struct {
					URL string `json:"url"`
				} `json:"resources"`
			} `json:"results"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(&body); err != nil {
		return Resource{}, fmt.Errorf("decode search result: %w", err)
	}
	for _, d := range body.Result.Results {
		if d.Name != datasetName || len(d.Resources) == 0 {
			continue
		}
		return Resource{
			DatasetName: d.Name,
			Title:       d.Title,
			Publisher:   d.Organization.Title,
			License:     d.LicenseTitle,
			PageURL:     "https://opendata.az/dataset/" + d.Name,
			FileURL:     d.Resources[0].URL,
			Modified:    d.MetadataModified,
		}, nil
	}
	return Resource{}, fmt.Errorf("dataset %q not found on opendata.az", datasetName)
}

// Download fetches a file with a size limit.
func Download(ctx context.Context, c *http.Client, fileURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(fileURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("refusing non-HTTP URL %q", fileURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", fileURL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, errors.New("download exceeds size limit")
	}
	return b, nil
}

// NewHTTPClient returns a client with sane timeouts for the import.
func NewHTTPClient() *http.Client { return &http.Client{Timeout: 3 * time.Minute} }
