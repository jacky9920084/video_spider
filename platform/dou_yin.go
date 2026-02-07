package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type DouYinPlatform struct {
	Record Record
}

var douyinUserAgents = []string{
	"Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 13; SM-S908B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/112.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
}

// VideoJson 响应数据
type VideoJson struct {
	LoaderData struct {
		VideoPage struct {
			VideoInfoRes struct {
				ItemList []struct {
					Desc   string `json:"desc"`
					Images []struct {
						UrlList []string `json:"url_list"`
					} `json:"images"`
					Video struct {
						Player struct {
							Uri     string   `json:"uri"`
							UrlList []string `json:"url_list"`
						} `json:"play_addr"`
						Cover struct {
							UrlList []string `json:"url_list"`
						} `json:"cover"`
					} `json:"video"`
				} `json:"item_list"`
			} `json:"videoInfoRes"`
		} `json:"video_(id)/page"`
		NotePage struct {
			VideoInfoRes struct {
				ItemList []struct {
					Desc   string `json:"desc"`
					Images []struct {
						UrlList []string `json:"url_list"`
					} `json:"images"`
				} `json:"item_list"`
			} `json:"videoInfoRes"`
		} `json:"note_(id)/page"`
	} `json:"loaderData"`
}

func sanitizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'")
	raw = strings.TrimRight(raw, "）).,，。!！?？;；")
	return raw
}

func extractAwemeID(u *url.URL) string {
	if u == nil {
		return ""
	}
	if modalID := u.Query().Get("modal_id"); modalID != "" {
		return modalID
	}
	if strings.Contains(u.Path, "/share/video/") {
		parts := strings.Split(u.Path, "/share/video/")
		if len(parts) == 2 {
			id := strings.Trim(parts[1], "/")
			if regexp.MustCompile(`^\d+$`).MatchString(id) {
				return id
			}
		}
	}
	if strings.HasPrefix(u.Path, "/video/") {
		id := strings.Trim(strings.TrimPrefix(u.Path, "/video/"), "/")
		if regexp.MustCompile(`^\d+$`).MatchString(id) {
			return id
		}
	}
	return ""
}

func newHTTPClient(jar http.CookieJar, noRedirect bool) *http.Client {
	client := &http.Client{
		Jar:     jar,
		Timeout: 20 * time.Second,
	}
	if noRedirect {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client
}

func setDouyinHeaders(req *http.Request, userAgent string) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Referer", "https://www.douyin.com/")
	req.Header.Set("Origin", "https://www.douyin.com")
	if cookie := strings.TrimSpace(os.Getenv("DOUYIN_COOKIE")); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
}

func looksBlocked(html string) bool {
	// 抖音页面里可能会混入验证码相关脚本，但仍然包含可解析的数据；
	// 这里只拦截明确的“JS 校验/中间页”内容。
	return strings.Contains(html, "window.byted_acrawler") ||
		strings.Contains(html, "._$jsvmprt") ||
		strings.Contains(html, "验证码中间页")
}

func resolveDouyinShortLink(shortURL string, userAgent string) (string, error) {
	jar, _ := cookiejar.New(nil)
	client := newHTTPClient(jar, true)
	req, err := http.NewRequest("GET", shortURL, nil)
	if err != nil {
		return "", err
	}
	setDouyinHeaders(req, userAgent)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	_ = res.Body.Close()

	location := res.Header.Get("Location")
	if location == "" {
		return "", errors.New("短链未返回重定向地址")
	}

	locURL, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	if locURL.Scheme == "" {
		baseURL, baseErr := url.Parse(shortURL)
		if baseErr != nil {
			return "", err
		}
		location = baseURL.ResolveReference(locURL).String()
	}

	return location, nil
}

func (dy DouYinPlatform) ParseOut() (record Record, err error) {

	dy.Record.Link = sanitizeURL(dy.Record.Link)
	parsedURL, _ := url.Parse(dy.Record.Link)

	// 1) 先从输入里提 aweme_id；短链需要先解一次 302
	awemeID := extractAwemeID(parsedURL)
	if awemeID == "" && parsedURL != nil && strings.Contains(parsedURL.Host, "v.douyin.com") {
		for _, userAgent := range douyinUserAgents {
			resolved, resolveErr := resolveDouyinShortLink(dy.Record.Link, userAgent)
			if resolveErr != nil {
				continue
			}
			resolvedURL, _ := url.Parse(resolved)
			awemeID = extractAwemeID(resolvedURL)
			if awemeID != "" {
				break
			}
		}
	}
	if awemeID == "" {
		return Record{}, errors.New("抖音解析失败：未识别到 aweme_id（支持 v.douyin.com 短链、/video/<id>、/share/video/<id>、jingxuan?modal_id=<id>）")
	}

	// 2) 优先尝试 m 端 share 页（当前网络环境命中率更高），再回退 iesdouyin
	shareURLs := []string{
		fmt.Sprintf("https://m.douyin.com/share/video/%s", awemeID),
		fmt.Sprintf("https://www.iesdouyin.com/share/video/%s/", awemeID),
	}
	blockedDetected := false

	for _, shareURL := range shareURLs {
		for _, userAgent := range douyinUserAgents {
			jar, jarErr := cookiejar.New(nil)
			if jarErr != nil {
				err = jarErr
				return
			}
			client := newHTTPClient(jar, false)
			noRedirectClient := newHTTPClient(jar, true)

			req, reqErr := http.NewRequest("GET", shareURL, nil)
			if reqErr != nil {
				err = reqErr
				return
			}
			setDouyinHeaders(req, userAgent)
			htmlRes, doErr := client.Do(req)
			if doErr != nil {
				continue
			}
			bodyBytes, readErr := io.ReadAll(htmlRes.Body)
			_ = htmlRes.Body.Close()
			if readErr != nil {
				continue
			}
			html := string(bodyBytes)
			if looksBlocked(html) {
				blockedDetected = true
				continue
			}

			// 加载html内容, 查找视频资源信息
			doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(html))
			if docErr != nil {
				continue
			}
			jsonData := ""
			doc.Find("script").Each(func(i int, s *goquery.Selection) {
				scriptText := s.Text()
				if jsonData == "" && strings.Contains(scriptText, "window._ROUTER_DATA") {
					start := strings.Index(scriptText, "{")
					end := strings.LastIndex(scriptText, "}") + 1
					if start >= 0 && end > start {
						jsonData = scriptText[start:end]
					}
				}
				if jsonData == "" && strings.Contains(scriptText, "window.__INITIAL_STATE__") {
					start := strings.Index(scriptText, "{")
					end := strings.LastIndex(scriptText, "}") + 1
					if start >= 0 && end > start {
						jsonData = scriptText[start:end]
					}
				}
			})
			if jsonData == "" {
				if strings.Contains(html, "_wafchallengeid") || strings.Contains(html, "验证码中间页") {
					blockedDetected = true
				}
				continue
			}

			var js json.RawMessage
			isJson := json.Unmarshal([]byte(jsonData), &js) == nil
			if !isJson {
				continue
			}
			videoJson := VideoJson{}
			if unmarshalErr := json.Unmarshal([]byte(jsonData), &videoJson); unmarshalErr != nil {
				continue
			}

			// 验证数据合法
			noteItemList := videoJson.LoaderData.NotePage.VideoInfoRes.ItemList
			videoItemList := videoJson.LoaderData.VideoPage.VideoInfoRes.ItemList
			if len(noteItemList) < 1 && len(videoItemList) < 1 {
				continue
			}

			// 图文资源 -- 图文两种情况
			if len(videoItemList) > 0 && len(videoItemList[0].Images) > 0 {
				var imageResource []string
				for _, v := range videoItemList[0].Images {
					imageResource = append(imageResource, v.UrlList[0])
				}
				dy.Record.Type = 2
				dy.Record.Cover = videoItemList[0].Video.Cover.UrlList[0]
				dy.Record.Title = videoItemList[0].Desc
				dy.Record.ResourcePath = imageResource
			}
			if len(noteItemList) > 0 {
				var imageResource []string
				for _, v := range noteItemList[0].Images {
					imageResource = append(imageResource, v.UrlList[0])
				}
				dy.Record.Type = 2
				dy.Record.Title = noteItemList[0].Desc
				dy.Record.Cover = noteItemList[0].Images[0].UrlList[0]
				dy.Record.ResourcePath = imageResource
			}

			// 视频资源
			if len(videoItemList) > 0 && len(videoItemList[0].Images) == 0 {

				videoID := videoItemList[0].Video.Player.Uri
				if videoID == "" {
					continue
				}

				redirectURL := fmt.Sprintf("https://aweme.snssdk.com/aweme/v1/play/?video_id=%s&ratio=720p&line=0", videoID)
				redirectReq, redirectReqErr := http.NewRequest("GET", redirectURL, nil)
				if redirectReqErr != nil {
					continue
				}
				setDouyinHeaders(redirectReq, userAgent)
				redirectRes, headErr := noRedirectClient.Do(redirectReq)
				if headErr != nil {
					continue
				}
				_ = redirectRes.Body.Close()
				resourceURL := redirectRes.Header.Get("Location")
				if resourceURL == "" && len(videoItemList[0].Video.Player.UrlList) > 0 {
					resourceURL = videoItemList[0].Video.Player.UrlList[0]
				}

				dy.Record.Type = 1
				dy.Record.Title = videoItemList[0].Desc
				dy.Record.Cover = videoItemList[0].Video.Cover.UrlList[0]
				dy.Record.Video = redirectURL
				dy.Record.ResourcePath = resourceURL
			}

			if dy.Record.Type != 0 {
				return dy.Record, nil
			}
		}
	}

	if blockedDetected {
		return Record{}, errors.New("抖音解析失败：当前网络/IP 触发风控或验证码。建议改用 v.douyin.com 分享短链，或配置环境变量 DOUYIN_COOKIE 后重试")
	}
	return Record{}, errors.New("抖音解析失败：未提取到可用数据（页面结构可能变更）")
}
