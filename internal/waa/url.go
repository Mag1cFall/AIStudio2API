package waa

import (
	cryptorand "crypto/rand"
	"fmt"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
	whatwgurl "github.com/nlnwa/whatwg-url/url"
)

// urlParser 按 WHATWG URL 标准解析地址
var urlParser = whatwgurl.NewParser()

// urlRecord 返回 URL 接口各属性的取值
func urlRecord(address *whatwgurl.Url) map[string]any {
	return map[string]any{
		"href": address.Href(false), "origin": urlOrigin(address), "protocol": address.Protocol(),
		"username": address.Username(), "password": address.Password(), "host": address.Host(),
		"hostname": address.Hostname(), "port": address.Port(), "pathname": address.Pathname(),
		"search": address.Search(), "hash": address.Hash(),
	}
}

// urlOrigin 按 WHATWG URL 标准返回地址的源，不透明源为 null
func urlOrigin(address *whatwgurl.Url) string {
	switch address.Scheme() {
	case "http", "https", "ws", "wss", "ftp":
		return address.Protocol() + "//" + address.Host()
	case "blob":
		inner, err := urlParser.Parse(address.Pathname())
		if err == nil && (inner.Scheme() == "http" || inner.Scheme() == "https") {
			return urlOrigin(inner)
		}
	}
	return "null"
}

// parseURL 以可选基址解析地址，无效时返回 nil
func parseURL(input string, base goja.Value) map[string]any {
	var address *whatwgurl.Url
	var err error
	if base == nil || goja.IsUndefined(base) {
		address, err = urlParser.Parse(input)
	} else {
		address, err = urlParser.ParseRef(base.String(), input)
	}
	if err != nil {
		return nil
	}
	return urlRecord(address)
}

// updateURL 按 URL 接口的 setter 修改地址的一个属性，href 赋值无效时返回 nil
func updateURL(href, field, value string) map[string]any {
	address, err := urlParser.Parse(href)
	if err != nil {
		return nil
	}
	switch field {
	case "href":
		if address, err = urlParser.Parse(value); err != nil {
			return nil
		}
	case "protocol":
		address.SetProtocol(value)
	case "username":
		address.SetUsername(value)
	case "password":
		address.SetPassword(value)
	case "host":
		address.SetHost(value)
	case "hostname":
		address.SetHostname(value)
	case "port":
		address.SetPort(value)
	case "pathname":
		address.SetPathname(value)
	case "search":
		address.SetSearch(value)
	case "hash":
		address.SetHash(value)
	}
	return urlRecord(address)
}

// randomUUID 返回版本 4 的随机 UUID
func randomUUID() string {
	var value [16]byte
	_, _ = cryptorand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:])
}
