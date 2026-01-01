package main

import (
	"errors"
	"github.com/labstack/echo/v4"
	"net/http"
	"regexp"
	"strings"
	"video_spider/platform"
)

func hello(c echo.Context) error {
	return success(c, "Hello, World!")
}

func analysis(c echo.Context) error {
	// 解析链接
	linkText := c.FormValue("share_link")
	if linkText == "" {
		return errors.New("分享链接不能为空")
	}

	links := regexp.MustCompile(`https?://\S+`).FindAllString(linkText, -1)
	if len(links) == 0 {
		return errors.New("未找到有效链接")
	}

	// 解析链接匹配解析器, 绑定结果到Model
	var err error
	var record platform.Record
	parsed := false
	for _, link := range links {
		switch {
		case strings.Contains(link, "v.douyin.com") || strings.Contains(link, "douyin.com"):
			record, err = platform.DouYinPlatform{
				Record: platform.Record{Link: link},
			}.ParseOut()
		case strings.Contains(link, "h5.pipix.com"):
			record, err = platform.PiPiXiaPlatform{
				Record: platform.Record{Link: link},
			}.ParseOut()
		case strings.Contains(link, "isee.weishi.qq.com"):
			record, err = platform.WeiShiPlatform{
				Record: platform.Record{Link: link},
			}.ParseOut()
		case strings.Contains(link, "xhslink.com"):
			record, err = platform.RedBookPlatform{
				Record: platform.Record{Link: link},
			}.ParseOut()
		case strings.Contains(link, "www.bilibili.com") || strings.Contains(link, "b23.tv"):
			record, err = platform.BiliBiliPlatform{
				Record: platform.Record{Link: link},
			}.ParseOut()
		case strings.Contains(link, "kuaishou.com"):
			record, err = platform.QuickShouPlatform{
				Record: platform.Record{Link: link},
			}.ParseOut()
		default:
			continue
		}
		if err != nil {
			continue
		}
		if record.Type == 0 {
			err = errors.New("解析失败：未提取到资源（可能触发风控/验证码或链接类型不支持，建议改用分享短链）")
			continue
		}
		parsed = true
		break
	}
	if err != nil {
		return err
	}
	if !parsed {
		return errors.New("暂不支持该平台资源解析")
	}

	return success(c, map[string]interface{}{
		"type":          record.Type,
		"title":         record.Title,
		"cover":         record.Cover,
		"video":         record.Video,
		"resource_path": record.ResourcePath,
	})
}

// Success 响应封装
func success(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusOK, map[string]interface{}{
		"data":    data,
		"message": "success",
	})
}
