package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	baseURL   = "https://ykt.qhu.edu.cn"
	userAgent = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Mobile Safari/537.36 MicroMessenger/8.0.48.2560(0x28003039) WeChat/arm64 Weixin NetType/WIFI Language/zh_CN"
)

var hiddenInputPatterns = map[string]*regexp.Regexp{
	"idserial": regexp.MustCompile(`(?s)<input[^>]*\bid="idserial"[^>]*\bvalue="([^"]*)"`),
	"username": regexp.MustCompile(`(?s)<input[^>]*\bid="username"[^>]*\bvalue="([^"]*)"`),
	"tel":      regexp.MustCompile(`(?s)<input[^>]*\bid="tel"[^>]*\bvalue="([^"]*)"`),
}

type pageFields struct {
	IDSerial string
	Username string
	Tel      string
}

type eleLastBind struct {
	FloorID     string `json:"floorid"`
	FactoryCode string `json:"factorycode"`
	RoomID      string `json:"roomid"`
	BuildingID  string `json:"buildingid"`
}

type userLastInfoResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	ResultData struct {
		EleLastBind string `json:"elelastbind"`
	} `json:"resultData"`
}

type registerResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	ResultData struct {
		RegisterID string `json:"regidterid"`
	} `json:"resultData"`
}

type roomResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	ResultData struct {
		Quantity    float64 `json:"quantity"`
		Description string  `json:"description"`
		RoomVerify  string  `json:"roomverify"`
		CanBuy      *int    `json:"canBuy"`
	} `json:"resultData"`
}

type result struct {
	FetchedAt   string  `json:"fetched_at"`
	Electricity string  `json:"electricity"`
	Quantity    float64 `json:"quantity"`
	Room        string  `json:"room"`
	RoomID      string  `json:"room_id"`
	FactoryCode string  `json:"factory_code"`
	CanBuy      *int    `json:"can_buy"`
}

type client struct {
	openID string
	http   *http.Client
}

func main() {
	openIDFlag := flag.String("openid", "", "Campus card openid. Falls back to OPENID env var.")
	roomIDFlag := flag.String("roomid", "", "Optional room id override. If empty, use saved binding.")
	factoryCodeFlag := flag.String("factorycode", "", "Optional factory code override. If empty, use saved binding.")
	jsonOutput := flag.Bool("json", false, "Print JSON instead of text.")
	timeout := flag.Duration("timeout", 20*time.Second, "HTTP timeout.")
	flag.Parse()

	openID := strings.TrimSpace(*openIDFlag)
	if openID == "" {
		openID = strings.TrimSpace(os.Getenv("OPENID"))
	}
	if openID == "" {
		exitf("missing openid; use -openid or OPENID")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		exitErr(err)
	}

	httpClient := &http.Client{
		Jar:     jar,
		Timeout: *timeout,
	}

	c := &client{
		openID: openID,
		http:   httpClient,
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res, err := c.fetchElectricity(ctx, strings.TrimSpace(*roomIDFlag), strings.TrimSpace(*factoryCodeFlag))
	if err != nil {
		exitErr(err)
	}

	if *jsonOutput {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			exitErr(err)
		}
		fmt.Println(string(data))
		return
	}

	fmt.Printf("剩余电量: %s\n", res.Electricity)
	fmt.Printf("房间: %s\n", res.Room)
}

func (c *client) fetchElectricity(ctx context.Context, roomIDOverride, factoryCodeOverride string) (*result, error) {
	if _, err := c.get(ctx, "/home/openHomePage", url.Values{"openid": {c.openID}}); err != nil {
		return nil, err
	}

	elePageQuery := url.Values{
		"openid":      {c.openID},
		"displayflag": {"1"},
		"id":          {"30"},
	}
	elePageHTML, err := c.get(ctx, "/elepay/openElePay", elePageQuery)
	if err != nil {
		return nil, err
	}

	fields, err := parsePageFields(elePageHTML)
	if err != nil {
		return nil, err
	}

	lastInfo, err := c.queryLastInfo(ctx, fields.IDSerial)
	if err != nil {
		return nil, err
	}

	binding := eleLastBind{}
	if lastInfo.ResultData.EleLastBind != "" {
		if err := json.Unmarshal([]byte(lastInfo.ResultData.EleLastBind), &binding); err != nil {
			return nil, fmt.Errorf("parse elelastbind: %w", err)
		}
	}

	roomID := firstNonEmpty(roomIDOverride, binding.RoomID)
	factoryCode := firstNonEmpty(factoryCodeOverride, binding.FactoryCode)
	if roomID == "" || factoryCode == "" {
		return nil, errors.New("no saved room binding found; pass -roomid and -factorycode to override")
	}

	if _, err := c.register(ctx, fields, factoryCode); err != nil {
		return nil, err
	}

	roomData, err := c.queryRoom(ctx, roomID, factoryCode)
	if err != nil {
		return nil, err
	}

	return &result{
		FetchedAt:   time.Now().Format(time.RFC3339),
		Electricity: fmt.Sprintf("%g度", roomData.ResultData.Quantity),
		Quantity:    roomData.ResultData.Quantity,
		Room:        roomData.ResultData.Description,
		RoomID:      roomData.ResultData.RoomVerify,
		FactoryCode: factoryCode,
		CanBuy:      roomData.ResultData.CanBuy,
	}, nil
}

func (c *client) queryLastInfo(ctx context.Context, idSerial string) (*userLastInfoResponse, error) {
	payload := map[string]string{
		"idserial": idSerial,
		"openid":   c.openID,
	}
	var out userLastInfoResponse
	if err := c.postJSON(ctx, "/myaccount/querywechatUserLastInfo", payload, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("query last bind failed: %s", out.Message)
	}
	return &out, nil
}

func (c *client) register(ctx context.Context, fields pageFields, factoryCode string) (*registerResponse, error) {
	payload := map[string]string{
		"factorycode": factoryCode,
		"idserial":    fields.IDSerial,
		"username":    fields.Username,
		"tel":         fields.Tel,
	}
	var out registerResponse
	if err := c.postJSON(ctx, "/channel/queryFloorList", payload, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("register failed: %s", out.Message)
	}
	return &out, nil
}

func (c *client) queryRoom(ctx context.Context, roomID, factoryCode string) (*roomResponse, error) {
	payload := map[string]string{
		"roomid":      roomID,
		"factorycode": factoryCode,
	}
	var out roomResponse
	if err := c.postJSON(ctx, "/channel/queryRoomList", payload, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("query room failed: %s", out.Message)
	}
	if out.ResultData.CanBuy != nil && *out.ResultData.CanBuy == 0 {
		return nil, errors.New("room returned canBuy=0")
	}
	return &out, nil
}

func (c *client) get(ctx context.Context, path string, query url.Values) (string, error) {
	u := baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s returned %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}

	return string(body), nil
}

func (c *client) postJSON(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	u := fmt.Sprintf("%s%s?openid=%s", baseURL, path, url.QueryEscape(c.openID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-requested-with", "XMLHttpRequest")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s returned %d: %s", path, resp.StatusCode, truncate(string(respBody), 200))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode %s response: %w; body=%s", path, err, truncate(string(respBody), 200))
	}
	return nil
}

func (c *client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
}

func parsePageFields(htmlBody string) (pageFields, error) {
	fields := pageFields{}
	for key, re := range hiddenInputPatterns {
		match := re.FindStringSubmatch(htmlBody)
		if len(match) != 2 {
			return pageFields{}, fmt.Errorf("failed to find %s in elepay page", key)
		}
		value := html.UnescapeString(match[1])
		switch key {
		case "idserial":
			fields.IDSerial = value
		case "username":
			fields.Username = value
		case "tel":
			fields.Tel = value
		}
	}
	return fields, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
