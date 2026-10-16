package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// ---------- IGDB ----------

type IGDBResult struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Year     int    `json:"year"`
	Platform string `json:"platform"`
	CoverURL string `json:"coverUrl"`
}

type igdbClient struct {
	mu      sync.Mutex
	id      string
	token   string
	expires time.Time
}

func (c *igdbClient) accessToken(clientID, secret string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id == clientID && c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {secret},
		"grant_type":    {"client_credentials"},
	}
	resp, err := httpClient.PostForm("https://id.twitch.tv/oauth2/token", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Message     string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if resp.StatusCode != 200 || r.AccessToken == "" {
		return "", fmt.Errorf("Twitch login failed (HTTP %d): %s - check Client ID / Secret", resp.StatusCode, r.Message)
	}
	c.id = clientID
	c.token = r.AccessToken
	c.expires = time.Now().Add(time.Duration(r.ExpiresIn-300) * time.Second)
	return c.token, nil
}

func (c *igdbClient) invalidate() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

func (c *igdbClient) Search(clientID, secret, query string) ([]IGDBResult, error) {
	if clientID == "" || secret == "" {
		return nil, errors.New("set your Twitch Client ID and Client Secret in Settings to search IGDB")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	tok, err := c.accessToken(clientID, secret)
	if err != nil {
		return nil, err
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(query)
	body := fmt.Sprintf(`search "%s"; fields name,first_release_date,cover.image_id,platforms.abbreviation; limit 20;`, q)
	req, _ := http.NewRequest("POST", "https://api.igdb.com/v4/games", strings.NewReader(body))
	req.Header.Set("Client-ID", clientID)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		c.invalidate()
		return nil, errors.New("IGDB rejected the token, try again")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("IGDB error (HTTP %d): %s", resp.StatusCode, b)
	}
	var games []struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		First int64  `json:"first_release_date"`
		Cover struct {
			ImageID string `json:"image_id"`
		} `json:"cover"`
		Platforms []struct {
			Abbr string `json:"abbreviation"`
		} `json:"platforms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&games); err != nil {
		return nil, err
	}
	out := []IGDBResult{}
	for _, g := range games {
		r := IGDBResult{ID: g.ID, Name: g.Name}
		if g.First > 0 {
			r.Year = time.Unix(g.First, 0).UTC().Year()
		}
		var ps []string
		for _, p := range g.Platforms {
			if p.Abbr != "" {
				ps = append(ps, p.Abbr)
			}
		}
		r.Platform = strings.Join(ps, ", ")
		if g.Cover.ImageID != "" {
			r.CoverURL = "https://images.igdb.com/igdb/image/upload/t_cover_big/" + g.Cover.ImageID + ".jpg"
		}
		out = append(out, r)
	}
	return out, nil
}

// ---------- Local file upload ----------

const maxUpload = 10 << 20

var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

func readImage(path string) ([]byte, string, error) {
	mime, ok := imageTypes[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, "", errors.New("unsupported image type (use PNG, JPG, GIF or WEBP)")
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if st.Size() > maxUpload {
		return nil, "", errors.New("image is larger than 10 MB")
	}
	b, err := os.ReadFile(path)
	return b, mime, err
}

type UploadResult struct {
	URL        string `json:"url"`
	DeleteHash string `json:"deleteHash"`
}

func uploadImgur(clientID, path string) (UploadResult, error) {
	if clientID == "" {
		return UploadResult{}, errors.New("set your Imgur Client ID in Settings first")
	}
	data, _, err := readImage(path)
	if err != nil {
		return UploadResult{}, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("image", filepath.Base(path))
	fw.Write(data)
	w.WriteField("type", "file")
	w.Close()
	req, _ := http.NewRequest("POST", "https://api.imgur.com/3/image", &buf)
	req.Header.Set("Authorization", "Client-ID "+clientID)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := httpClient.Do(req)
	if err != nil {
		return UploadResult{}, err
	}
	defer resp.Body.Close()
	var r struct {
		Success bool `json:"success"`
		Data    struct {
			Link       string `json:"link"`
			DeleteHash string `json:"deletehash"`
			Error      any    `json:"error"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if !r.Success || r.Data.Link == "" {
		return UploadResult{}, fmt.Errorf("Imgur upload failed (HTTP %d): %v", resp.StatusCode, r.Data.Error)
	}
	return UploadResult{URL: r.Data.Link, DeleteHash: r.Data.DeleteHash}, nil
}

func uploadCatbox(path string) (UploadResult, error) {
	data, _, err := readImage(path)
	if err != nil {
		return UploadResult{}, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("reqtype", "fileupload")
	fw, _ := w.CreateFormFile("fileToUpload", filepath.Base(path))
	fw.Write(data)
	w.Close()
	req, _ := http.NewRequest("POST", "https://catbox.moe/user/api.php", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := httpClient.Do(req)
	if err != nil {
		return UploadResult{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
	link := strings.TrimSpace(string(b))
	if resp.StatusCode != 200 || !strings.HasPrefix(link, "https://") {
		return UploadResult{}, fmt.Errorf("catbox upload failed (HTTP %d): %s", resp.StatusCode, link)
	}
	return UploadResult{URL: link}, nil
}

func deleteImgur(clientID, hash string) error {
	req, _ := http.NewRequest("DELETE", "https://api.imgur.com/3/image/"+url.PathEscape(hash), nil)
	req.Header.Set("Authorization", "Client-ID "+clientID)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Imgur delete failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// validImageURL checks the URL is public https (Discord only accepts those).
func validImageURL(s string) error {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("image URL must be a public https:// link")
	}
	if len(s) > 256 {
		return errors.New("image URL is longer than 256 characters (Discord limit)")
	}
	return nil
}
