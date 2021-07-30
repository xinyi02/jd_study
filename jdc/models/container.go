package models

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"

	"github.com/astaxie/beego/httplib"
	"github.com/beego/beego/v2/core/logs"
	"github.com/buger/jsonparser"
)

const (
	QL = "ql"
	V4 = "v4"
	LI = "li"
)

func initContainer() {
	for i := range Config.Containers {
		if Config.Containers[i].Weigth == 0 {
			Config.Containers[i].Weigth = 1
		}
		switch Config.Containers[i].Type {
		case "ql":
			vv := regexp.MustCompile(`^(https?://[\.\w]+:?\d*)`).FindStringSubmatch(Config.Containers[i].Address)
			if len(vv) == 2 {
				Config.Containers[i].Address = vv[1]
			} else {
				logs.Warn("ql地址：%s错误", Config.Containers[i].Address)
			}
			version, err := GetQlVersion(Config.Containers[i].Address)
			if Config.Containers[i].getToken() == nil {
				logs.Info("ql" + version + "登录成功")
			} else {
				logs.Warn("ql" + version + "登录失败")
			}
			if err != nil {
				logs.Warn("ql版本识别失败")
			}
			Config.Containers[i].Version = version
		case "v4", "li":
			f, err := os.Open(Config.Containers[i].Path)
			if err != nil {
				logs.Warn("无法打开" + Config.Containers[i].Type + "配置文件，请检查路径是否正确")
			} else {
				f.Close()
				logs.Info(Config.Containers[i].Type + "配置文件正确")
			}
		}
	}
}

func (c *Container) write(cks []JdCookie) error {
	switch c.Type {
	case "ql":
		if c.Version == "2.8" {
			if len(c.Delete) > 0 {
				c.request("/api/envs", DELETE, fmt.Sprintf(`[%s]`, strings.Join(c.Delete, ",")))
			}
			d := ""
			if len(cks) != 0 {
				for _, ck := range cks {
					if ck.Available == True {
						d += fmt.Sprintf("pt_key=%s;pt_pin=%s;\\n", ck.PtKey, ck.PtPin)
					}
				}
				c.request("/api/envs", POST, `{"name":"JD_COOKIE","value":"`+d+`"}`)
			}
		} else {
			if len(c.Delete) > 0 {
				c.request("/api/cookies", DELETE, fmt.Sprintf(`[%s]`, strings.Join(c.Delete, ",")))
			}
			d := []string{}
			for _, ck := range cks {
				if ck.Available == True {
					d = append(d, fmt.Sprintf("\"pt_key=%s;pt_pin=%s;\"", ck.PtKey, ck.PtPin))
				}
			}
			if len(d) != 0 {
				c.request("/api/cookies", POST, fmt.Sprintf(`[%s]`, strings.Join(d, ",")))
			}
		}
	case "v4":
		config := ""
		f, err := os.OpenFile(c.Path, os.O_RDWR|os.O_CREATE, 0777) //打开文件 |os.O_RDWR
		if err != nil {
			return err
		}
		defer f.Close()
		rd := bufio.NewReader(f)
		for {
			line, err := rd.ReadString('\n') //以'\n'为结束符读入一行
			if err != nil || io.EOF == err {
				break
			}
			if pt := regexp.MustCompile(`^#?\s?Cookie(\d+)=\S+pt_key=(.*);pt_pin=([^'";\s]+);?`).FindStringSubmatch(line); len(pt) != 0 {
				continue
			}
			if pt := regexp.MustCompile(`^TempBlockCookie=`).FindString(line); pt != "" {
				continue
			}
			if pt := regexp.MustCompile(`^Cookie\d+=`).FindString(line); pt != "" {
				continue
			}
			config += line
		}
		TempBlockCookie := ""
		for i, ck := range cks {
			if ck.Available == False {
				TempBlockCookie += fmt.Sprintf("%d ", i+1)
			}
			config = fmt.Sprintf("Cookie%d=\"pt_key=%s;pt_pin=%s;\"\n", i+1, ck.PtKey, ck.PtPin) + config
		}
		config = fmt.Sprintf(`TempBlockCookie="%s"`, TempBlockCookie) + "\n" + config
		f.Truncate(0)
		f.Seek(0, 0)
		if _, err := io.WriteString(f, config); err != nil {
			return err
		}
		return nil
	case "li":
		config := ""
		f, err := os.OpenFile(c.Path, os.O_RDWR|os.O_CREATE, 0777) //打开文件 |os.O_RDWR
		if err != nil {
			return err
		}
		defer f.Close()
		rd := bufio.NewReader(f)
		for {
			line, err := rd.ReadString('\n') //以'\n'为结束符读入一行
			if err != nil || io.EOF == err {
				break
			}
			if pt := regexp.MustCompile(`^pt_key=(.*);pt_pin=([^'";\s]+);?`).FindStringSubmatch(line); len(pt) != 0 {
				continue
			}
			config += line
		}
		for _, ck := range cks {
			if ck.Available == True {
				config += fmt.Sprintf("pt_key=%s;pt_pin=%s\n", ck.PtKey, ck.PtPin)
			}
		}
		f.Truncate(0)
		f.Seek(0, 0)
		if _, err := io.WriteString(f, config); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func (c *Container) read() error {
	c.Available = true
	switch c.Type {
	case "ql":
		if c.Version == "2.8" {
			type AutoGenerated struct {
				Code int `json:"code"`
				Data []struct {
					Value     string  `json:"value"`
					ID        string  `json:"_id"`
					Created   int64   `json:"created"`
					Status    int     `json:"status"`
					Timestamp string  `json:"timestamp"`
					Position  float64 `json:"position"`
					Name      string  `json:"name"`
					Remarks   string  `json:"remarks,omitempty"`
				} `json:"data"`
			}
			var data, err = c.request("/api/envs?searchValue=JD_COOKIE")
			a := AutoGenerated{}
			err = json.Unmarshal(data, &a)
			if err != nil {
				c.Available = false
				return err
			}
			c.Delete = []string{}
			for _, env := range a.Data {
				c.Delete = append(c.Delete, fmt.Sprintf("\"%s\"", env.ID))
				res := regexp.MustCompile(`pt_key=(\S+);pt_pin=([^\s;]+);?`).FindAllStringSubmatch(env.Value, -1)
				for _, v := range res {
					if nck := GetJdCookie(v[2]); nck == nil {
						SaveJdCookie(JdCookie{
							PtKey:     v[1],
							PtPin:     v[2],
							Available: True,
						})
					} else {
						if nck.PtKey != v[1] {
							nck.Updates(map[string]interface{}{
								"PtKey":     v[1],
								"Available": True,
							})
						}
					}
				}
			}

			return nil
		} else {
			var data, err = c.request("/api/cookies")
			if err != nil {
				c.Available = false
				return err
			}
			type AutoGenerated struct {
				Code int `json:"code"`
				Data []struct {
					Value     string  `json:"value"`
					ID        string  `json:"_id"`
					Created   int64   `json:"created"`
					Status    int     `json:"status"`
					Timestamp string  `json:"timestamp"`
					Position  float64 `json:"position"`
					Nickname  string  `json:"nickname"`
				} `json:"data"`
			}
			var a = AutoGenerated{}
			c.Delete = []string{}
			json.Unmarshal(data, &a)
			for _, vv := range a.Data {
				c.Delete = append(c.Delete, fmt.Sprintf("\"%s\"", vv.ID))
				res := regexp.MustCompile(`pt_key=(\S+);pt_pin=([^\s;]+);?`).FindStringSubmatch(vv.Value)
				if len(res) == 3 {
					if nck := GetJdCookie(res[2]); nck == nil {
						SaveJdCookie(JdCookie{
							PtKey:     res[1],
							PtPin:     res[2],
							Available: True,
						})
					} else {
						if res[1] != nck.PtKey {
							nck.Updates(map[string]interface{}{
								"PtKey":     res[1],
								"Available": True,
							})
						}
					}
				}
			}
		}
	case "v4":
		f, err := os.OpenFile(c.Path, os.O_RDWR|os.O_CREATE, 0777) //打开文件 |os.O_RDWR
		if err != nil {
			c.Available = false
			return err
		}
		defer f.Close()
		rd := bufio.NewReader(f)
		for {
			line, err := rd.ReadString('\n') //以'\n'为结束符读入一行
			if err != nil || io.EOF == err {
				break
			}
			if pt := regexp.MustCompile(`^#?\s?Cookie(\d+)=\S+pt_key=(.*);pt_pin=([^'";\s]+);?`).FindStringSubmatch(line); len(pt) != 0 {
				if nck := GetJdCookie(pt[3]); nck == nil {
					SaveJdCookie(JdCookie{
						PtKey:     pt[2],
						PtPin:     pt[3],
						Available: True,
					})
				} else {
					if nck.PtKey != pt[2] {
						nck.Updates(map[string]interface{}{
							"PtKey":     pt[2],
							"Available": True,
						})
					}
				}
				continue
			}
		}
	case "li":
		f, err := os.OpenFile(c.Path, os.O_RDWR|os.O_CREATE, 0777) //打开文件 |os.O_RDWR
		if err != nil {
			c.Available = false
			return err
		}
		defer f.Close()
		rd := bufio.NewReader(f)
		for {
			line, err := rd.ReadString('\n') //以'\n'为结束符读入一行
			if err != nil || io.EOF == err {
				break
			}
			if pt := regexp.MustCompile(`^pt_key=(.*);pt_pin=([^'";\s]+);?`).FindStringSubmatch(line); len(pt) != 0 {
				if nck := GetJdCookie(pt[2]); nck == nil {
					SaveJdCookie(JdCookie{
						PtKey:     pt[1],
						PtPin:     pt[2],
						Available: True,
					})
				} else {
					if nck.PtKey != pt[1] {
						nck.Updates(map[string]interface{}{
							"PtKey":     pt[1],
							"Available": True,
						})
					}
				}
				continue
			}
		}
	}
	return nil
}

func (c *Container) getToken() error {
	req := httplib.Post(c.Address + "/api/login")
	req.Header("Content-Type", "application/json;charset=UTF-8")
	req.Body(fmt.Sprintf(`{"username":"%s","password":"%s"}`, c.Username, c.Password))
	if rsp, err := req.Response(); err == nil {
		data, err := ioutil.ReadAll(rsp.Body)
		if err != nil {
			return err
		}
		c.Token, _ = jsonparser.GetString(data, "token")
	} else {
		return err
	}
	return nil
}

func (c *Container) request(ss ...string) ([]byte, error) {
	var api, method, body string
	for _, s := range ss {
		if s == GET || s == POST || s == PUT || s == DELETE {
			method = s
		} else if strings.Contains(s, "/api/") {
			api = s
		} else {
			body = s
		}
	}
	var req *httplib.BeegoHTTPRequest
	var i = 0
	for {
		i++
		switch method {
		case POST:
			req = httplib.Post(c.Address + api)
		case PUT:
			req = httplib.Put(c.Address + api)
		case DELETE:
			req = httplib.Delete(c.Address + api)
		default:
			req = httplib.Get(c.Address + api)
		}
		req.Header("Authorization", "Bearer "+c.Token)
		if body != "" {
			req.Header("Content-Type", "application/json;charset=UTF-8")
			req.Body(body)
		}
		if data, err := req.Bytes(); err == nil {
			code, _ := jsonparser.GetInt(data, "code")
			if code == 200 {
				return data, nil
			} else {
				logs.Warn(string(data))
				if i >= 5 {
					return nil, errors.New("异常")
				}
				c.getToken()
			}
		}
	}
	return []byte{}, nil
}

func GetQlVersion(address string) (string, error) {
	data, err := httplib.Get(address).String()
	if err != nil {
		return "", err
	}
	js := regexp.MustCompile(`/umi\.\w+\.js`).FindString(data)
	if js == "" {
		return "", errors.New("好像不是青龙面板")
	}
	data, err = httplib.Get(address + js).String()
	if err != nil {
		return "", err
	}
	v := ""
	if strings.Contains(data, "v2.8") {
		v = "2.8"
	} else if strings.Contains(data, "v2.2") {
		v = "2.2"
	}
	return v, nil
}

const (
	GET    = "GET"
	POST   = "POST"
	PUT    = "PUT"
	DELETE = "DELETE"
)
