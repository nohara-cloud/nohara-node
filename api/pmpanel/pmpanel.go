package pmpanel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/go-resty/resty/v2"

	"github.com/nohara-cloud/nboard-node/api"
)

// APIClient create a api client to the panel.
type APIClient struct {
	client        *resty.Client
	APIHost       string
	NodeID        string
	Key           string
	NodeType      string
	EnableVless   bool
	VlessFlow     string
	SpeedLimit    float64
	DeviceLimit   int
	LocalRuleList []api.DetectRule
}

// New creat a api instance
func New(apiConfig *api.Config) *APIClient {

	client := resty.New()
	client.SetRetryCount(3)
	if apiConfig.Timeout > 0 {
		client.SetTimeout(time.Duration(apiConfig.Timeout) * time.Second)
	} else {
		client.SetTimeout(5 * time.Second)
	}
	client.OnError(func(req *resty.Request, err error) {
		if v, ok := err.(*resty.ResponseError); ok {
			// v.Response contains the last response from the server
			// v.Err contains the original error
			log.Print(v.Err)
		}
	})
	client.SetBaseURL(apiConfig.APIHost)
	// Add authorization key for each requests
	client.SetHeaders(map[string]string{
		"Authorization": apiConfig.Key,
	})
	nodeType := strings.ToLower(apiConfig.NodeType)
	if nodeType != "shadowsocks" && nodeType != "v2ray" && nodeType != "trojan" {
		return nil
	}
	log.Printf("Getting node info, Type: %s, NodeID: %s", nodeType, apiConfig.NodeID)
	client.SetQueryParams(map[string]string{
		"protocol": nodeType,
		"node_id":  apiConfig.NodeID,
	})
	// Read local rule list
	localRuleList := readLocalRuleList(apiConfig.RuleListPath)
	apiClient := &APIClient{
		client:        client,
		NodeID:        apiConfig.NodeID,
		Key:           apiConfig.Key,
		APIHost:       apiConfig.APIHost,
		NodeType:      apiConfig.NodeType,
		EnableVless:   apiConfig.EnableVless,
		VlessFlow:     apiConfig.VlessFlow,
		SpeedLimit:    apiConfig.SpeedLimit,
		DeviceLimit:   apiConfig.DeviceLimit,
		LocalRuleList: localRuleList,
	}
	return apiClient
}

// readLocalRuleList reads the local rule list file
func readLocalRuleList(path string) (LocalRuleList []api.DetectRule) {

	LocalRuleList = make([]api.DetectRule, 0)
	if path != "" {
		// open the file
		file, err := os.Open(path)

		// handle errors while opening
		if err != nil {
			log.Printf("Error when opening file: %s", err)
			return LocalRuleList
		}

		fileScanner := bufio.NewScanner(file)

		// read line by line
		for fileScanner.Scan() {
			LocalRuleList = append(LocalRuleList, api.DetectRule{
				ID:      -1,
				Pattern: regexp.MustCompile(fileScanner.Text()),
			})
		}
		// handle first encountered error while reading
		if err := fileScanner.Err(); err != nil {
			log.Fatalf("Error while reading file: %s", err)
			return
		}

		file.Close()
	}

	return LocalRuleList
}

// Describe return a description of the client
func (c *APIClient) Describe() api.ClientInfo {
	return api.ClientInfo{APIHost: c.APIHost, NodeID: c.NodeID, Key: c.Key, NodeType: c.NodeType}
}

// Debug set the client debug for client
func (c *APIClient) Debug() {
	c.client.SetDebug(true)
}

func (c *APIClient) assembleURL(path string) string {
	return c.APIHost + path
}

func (c *APIClient) parseResponse(res *resty.Response, path string, err error) ([]byte, error) {
	if err != nil {
		return nil, fmt.Errorf("request %s failed: %s", c.assembleURL(path), err)
	}

	if res.StatusCode() != 200 {
		body := res.Body()
		switch res.StatusCode() {
		case 400:
			return nil, fmt.Errorf("request %s failed with bad request: %s", c.assembleURL(path), string(body))
		case 401:
			return nil, fmt.Errorf("request %s failed with unauthorized: %s", c.assembleURL(path), string(body))
		case 403:
			return nil, fmt.Errorf("request %s failed with forbidden: %s", c.assembleURL(path), string(body))
		default:
			return nil, fmt.Errorf("request %s failed with status code %d: %s", c.assembleURL(path), res.StatusCode(), string(body))
		}
	}

	return res.Body(), nil
}

// GetNodeInfo will pull NodeInfo Config from sspanel
func (c *APIClient) GetNodeInfo() (nodeInfo *api.NodeInfo, err error) {
	path := fmt.Sprintf("/api/node/config")

	res, err := c.client.R().
		ForceContentType("application/json").
		Get(path)
	if err != nil {
		return nil, err
	}

	responseData, err := c.parseResponse(res, path, err)
	if err != nil {
		return nil, err
	}

	nodeInfoResponse := new(NodeInfoResponse)
	if err := json.Unmarshal(responseData, nodeInfoResponse); err != nil {
		return nil, fmt.Errorf("unmarshal %s failed: %s", reflect.TypeOf(nodeInfoResponse), err)
	}
	log.Printf("nodeInfoResponse: %+v", nodeInfoResponse)
	switch c.NodeType {
	case "V2ray":
		nodeInfo, err = c.ParseV2rayNodeResponse(nodeInfoResponse)
	case "Trojan":
		nodeInfo, err = c.ParseTrojanNodeResponse(nodeInfoResponse)
	case "Shadowsocks":
		nodeInfo, err = c.ParseSSNodeResponse(nodeInfoResponse)
	default:
		return nil, fmt.Errorf("unsupported Node type: %s", c.NodeType)
	}

	if err != nil {
		res, _ := json.Marshal(nodeInfoResponse)
		return nil, fmt.Errorf("Parse node info failed: %s, \nError: %s", string(res), err)
	}

	return nodeInfo, nil
}

// GetUserList will pull user from panel
func (c *APIClient) GetUserList() (UserList *[]api.UserInfo, err error) {
	path := "/api/node/user"
	res, err := c.client.R().
		ForceContentType("application/json").
		Get(path)
	log.Printf("[PMPanel] Get user list for node: %s", c.NodeID)
	responseData, err := c.parseResponse(res, path, err)
	if err != nil {
		return nil, err
	}

	var userListResponse *[]UserResponse
	if err := json.Unmarshal(responseData, &userListResponse); err != nil {
		return nil, fmt.Errorf("unmarshal %s failed: %s", reflect.TypeOf(userListResponse), err)
	}
	userList, err := c.ParseUserListResponse(userListResponse)
	if err != nil {
		res, _ := json.Marshal(userListResponse)
		return nil, fmt.Errorf("parse user list failed: %s", string(res))
	}
	return userList, nil
}

// ReportNodeStatus reports the node status to the sspanel
func (c *APIClient) ReportNodeStatus(nodeStatus *api.NodeStatus) (err error) {

	data := &NodeStatus{
		CPU:    nodeStatus.CPU,
		Mem:    nodeStatus.Mem,
		Disk:   nodeStatus.Disk,
		Uptime: nodeStatus.Uptime,
	}

	path := "/api/node/status"
	res, err := c.client.R().
		ForceContentType("application/json").
		SetBody(data).
		Post(path)

	_, err = c.parseResponse(res, path, err)
	if err != nil {
		return err
	}

	return nil
}

// ReportNodeOnlineUsers reports online user ip
func (c *APIClient) ReportNodeOnlineUsers(onlineUserList *[]api.OnlineUser) error {
	data := make([]OnlineUser, len(*onlineUserList))
	for i, user := range *onlineUserList {
		data[i] = OnlineUser{UID: user.UID, IP: user.IP}
	}

	postData := &OnlineUserPostData{Online: data}
	path := "/api/node/user/online"
	res, err := c.client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(postData).
		ForceContentType("application/json").
		Post(path)

	_, err = c.parseResponse(res, path, err)
	if err != nil {
		return err
	}

	return nil
}

// ReportUserTraffic reports the user traffic
func (c *APIClient) ReportUserTraffic(userTraffic *[]api.UserTraffic) error {
	data := make([]UserTraffic, len(*userTraffic))
	for i, traffic := range *userTraffic {
		data[i] = UserTraffic{
			UID:      traffic.UID,
			Upload:   traffic.Upload,
			Download: traffic.Download,
		}
	}

	postData := &TrafficPostData{Traffic: data}
	path := "/api/node/user/traffic"
	res, err := c.client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(postData).
		ForceContentType("application/json").
		Post(path)

	_, err = c.parseResponse(res, path, err)
	if err != nil {
		return err
	}

	return nil
}

// GetNodeRule will pull the audit rule form pmpanel
func (c *APIClient) GetNodeRule() (*[]api.DetectRule, error) {
	ruleList := c.LocalRuleList
	return &ruleList, nil
	// path := "/api/rules"
	// res, err := c.client.R().
	// 	SetResult(&Response{}).
	// 	ForceContentType("application/json").
	// 	Get(path)

	// response, err := c.parseResponse(res, path, err)
	// if err != nil {
	// 	return nil, err
	// }

	// ruleListResponse := new([]RuleItem)

	// if err := json.Unmarshal(response.Data, ruleListResponse); err != nil {
	// 	return nil, fmt.Errorf("unmarshal %s failed: %s", reflect.TypeOf(ruleListResponse), err)
	// }

	// for _, r := range *ruleListResponse {
	// 	ruleList = append(ruleList, api.DetectRule{
	// 		ID:      r.ID,
	// 		Pattern: regexp.MustCompile(r.Content),
	// 	})
	// }
	// return &ruleList, nil
}

// ReportIllegal reports the user illegal behaviors
func (c *APIClient) ReportIllegal(detectResultList *[]api.DetectResult) error {
	return nil
}

// ParseV2rayNodeResponse parse the response for the given nodeinfor format
func (c *APIClient) ParseV2rayNodeResponse(nodeInfoResponse *NodeInfoResponse) (*api.NodeInfo, error) {
	// var enableTLS bool
	// var path, host, transportProtocol, serviceName string
	// var speedLimit uint64 = 0

	// port := nodeInfoResponse.Port
	// alterID := nodeInfoResponse.AlterId
	// transportProtocol = nodeInfoResponse.Network
	// switch transportProtocol {
	// case "ws":
	// 	host = nodeInfoResponse.Host
	// 	path = nodeInfoResponse.Path
	// case "grpc":
	// 	serviceName = nodeInfoResponse.Sni
	// case "tcp":
	// 	// TODO
	// }
	// // Compatible with more node types config
	// switch nodeInfoResponse.Security {
	// case "tls":
	// 	enableTLS = true
	// default:
	// 	enableTLS = false
	// }
	// if c.SpeedLimit > 0 {
	// 	speedLimit = uint64((c.SpeedLimit * 1000000) / 8)
	// } else {
	// 	speedLimit = uint64((nodeInfoResponse.SpeedLimit * 1000000) / 8)
	// }
	// // Create GeneralNodeInfo
	// nodeinfo := &api.NodeInfo{
	// 	NodeType:          c.NodeType,
	// 	NodeID:            c.NodeID,
	// 	Port:              port,
	// 	SpeedLimit:        speedLimit,
	// 	AlterID:           alterID,
	// 	TransportProtocol: transportProtocol,
	// 	EnableTLS:         enableTLS,
	// 	Path:              path,
	// 	Host:              host,
	// 	EnableVless:       c.EnableVless,
	// 	VlessFlow:         c.VlessFlow,
	// 	ServiceName:       serviceName,
	// }

	// return nodeinfo, nil
	return nil, nil
}

// ParseSSNodeResponse parse the response for the given nodeinfor format
func (c *APIClient) ParseSSNodeResponse(nodeInfoResponse *NodeInfoResponse) (*api.NodeInfo, error) {
	var speedLimit uint64 = 0

	if c.SpeedLimit > 0 {
		speedLimit = uint64((c.SpeedLimit * 1000000) / 8)
	} else {
		speedLimit = uint64((nodeInfoResponse.SpeedLimit * 1000000) / 8)
	}
	// Create GeneralNodeInfo
	nodeInfo := &api.NodeInfo{
		NodeType:          c.NodeType,
		NodeID:            c.NodeID,
		Port:              nodeInfoResponse.Port,
		SpeedLimit:        speedLimit,
		TransportProtocol: "tcp",
		CypherMethod:      nodeInfoResponse.Method,
		ServerKey:         nodeInfoResponse.ServerKey,
	}

	return nodeInfo, nil
}

// ParseTrojanNodeResponse parse the response for the given nodeinfor format
func (c *APIClient) ParseTrojanNodeResponse(nodeInfoResponse *NodeInfoResponse) (*api.NodeInfo, error) {
	// // 域名或IP;port=连接端口#偏移端口|host=xx
	// // gz.aaa.com;port=443#12345|host=hk.aaa.com
	// var host string
	// var transportProtocol = "tcp"
	// var speedlimit uint64 = 0
	// host = nodeInfoResponse.Host
	// port := nodeInfoResponse.Port

	// if c.SpeedLimit > 0 {
	// 	speedlimit = uint64((c.SpeedLimit * 1000000) / 8)
	// } else {
	// 	speedlimit = uint64((nodeInfoResponse.SpeedLimit * 1000000) / 8)
	// }
	// if nodeInfoResponse.Grpc {
	// 	transportProtocol = "grpc"
	// }
	// // Create GeneralNodeInfo
	// nodeInfo := &api.NodeInfo{
	// 	NodeType:          c.NodeType,
	// 	NodeID:            c.NodeID,
	// 	Port:              port,
	// 	SpeedLimit:        speedlimit,
	// 	TransportProtocol: transportProtocol,
	// 	EnableTLS:         true,
	// 	Host:              host,
	// 	ServiceName:       nodeInfoResponse.Sni,
	// }

	// return nodeInfo, nil
	return nil, nil
}

// ParseUserListResponse parse the response for the given nodeinfo format
func (c *APIClient) ParseUserListResponse(userInfoResponse *[]UserResponse) (*[]api.UserInfo, error) {
	var deviceLimit = 0
	var speedLimit uint64 = 0
	userList := make([]api.UserInfo, len(*userInfoResponse))
	for i, user := range *userInfoResponse {
		if c.DeviceLimit > 0 {
			deviceLimit = c.DeviceLimit
		} else {
			deviceLimit = user.DeviceLimit
		}
		if c.SpeedLimit > 0 {
			speedLimit = uint64((c.SpeedLimit * 1000000) / 8)
		} else {
			speedLimit = uint64((user.SpeedLimit * 1000000) / 8)
		}
		userList[i] = api.UserInfo{
			UID:         user.ID,
			Passwd:      user.Passwd,
			UUID:        user.Passwd,
			SpeedLimit:  speedLimit,
			DeviceLimit: deviceLimit,
		}
	}

	return &userList, nil
}
