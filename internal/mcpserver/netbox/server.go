package netbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/graphql"
)

// Config holds the configuration for the Netbox MCP server.
type Config struct {
	// BaseURL is the base URL of the Netbox instance, e.g. https://netbox.example.com
	// No trailing slash.
	BaseURL string
	// Token is the Netbox API token.
	Token string
	// Timeout is the per-request HTTP timeout. Defaults to 30s.
	Timeout time.Duration
}

// Server implements an MCP server for Netbox DCIM/IPAM. It composes a
// *graphql.Server for all HTTP execution — no direct HTTP client is needed.
type Server struct {
	mcpserver.BaseServer
	gql      *graphql.Server
	handlers map[string]mcpserver.ToolHandler
}

// NewServer creates a new Netbox MCP server from the supplied Config.
func NewServer(cfg Config) (*Server, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base-url is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	gqlServer := graphql.NewServer(graphql.Config{
		Endpoint: strings.TrimRight(cfg.BaseURL, "/") + "/graphql/",
		Headers: map[string]string{
			"Authorization": "Token " + cfg.Token,
		},
		Timeout:          cfg.Timeout,
		AllowMutations:   false,
		AllowIntrospect:  true,
		MaxResponseBytes: 5 * 1024 * 1024,
	})

	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: "mcp-netbox", Version: "0.1.0"},
		gql:        gqlServer,
		handlers: make(map[string]mcpserver.ToolHandler),
	}

	s.handlers["netbox_query"] = s.handleQuery
	s.handlers["netbox_get_devices"] = s.handleGetDevices
	s.handlers["netbox_get_ip_addresses"] = s.handleGetIPAddresses
	s.handlers["netbox_get_prefixes"] = s.handleGetPrefixes
	s.handlers["netbox_get_sites"] = s.handleGetSites
	s.handlers["netbox_get_vlans"] = s.handleGetVLANs
	s.handlers["netbox_get_interfaces"] = s.handleGetInterfaces
	s.handlers["netbox_introspect"] = s.handleIntrospect

	return s, nil
}

// ListTools returns all available Netbox tools.
func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "netbox_query",
			Description: "Execute a raw GraphQL query against the Netbox API. Use this as an escape hatch for queries not covered by the convenience tools.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The GraphQL query string",
					},
					"variables": map[string]interface{}{
						"type":        "string",
						"description": "Optional JSON object of query variables",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "netbox_get_devices",
			Description: "List devices from Netbox DCIM. Filter by name, site, rack, role, status, or tenant.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Filter by device name (case-insensitive contains)",
					},
					"site": map[string]interface{}{
						"type":        "string",
						"description": "Filter by site name (case-insensitive contains)",
					},
					"rack": map[string]interface{}{
						"type":        "string",
						"description": "Filter by rack name (case-insensitive contains)",
					},
					"role": map[string]interface{}{
						"type":        "string",
						"description": "Filter by device role name (case-insensitive contains)",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status: active, planned, staged, failed, inventory, decommissioning, offline",
						"enum":        []string{"active", "planned", "staged", "failed", "inventory", "decommissioning", "offline"},
					},
					"tenant": map[string]interface{}{
						"type":        "string",
						"description": "Filter by tenant name (case-insensitive contains)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results (default: 25)",
					},
				},
			},
		},
		{
			Name:        "netbox_get_ip_addresses",
			Description: "List IP addresses from Netbox IPAM. Filter by address, status, or assigned device.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"address": map[string]interface{}{
						"type":        "string",
						"description": "Filter by IP address or prefix (e.g. 192.168.1.0/24)",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status",
					},
					"device": map[string]interface{}{
						"type":        "string",
						"description": "Filter by assigned device name (case-insensitive contains)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results (default: 25)",
					},
				},
			},
		},
		{
			Name:        "netbox_get_prefixes",
			Description: "List IP prefixes from Netbox IPAM. Filter by prefix, status, VRF, site, or tenant.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prefix": map[string]interface{}{
						"type":        "string",
						"description": "Filter by prefix (e.g. 10.0.0.0/8)",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status",
					},
					"vrf": map[string]interface{}{
						"type":        "string",
						"description": "Filter by VRF name (case-insensitive contains)",
					},
					"site": map[string]interface{}{
						"type":        "string",
						"description": "Filter by site name (case-insensitive contains)",
					},
					"tenant": map[string]interface{}{
						"type":        "string",
						"description": "Filter by tenant name (case-insensitive contains)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results (default: 25)",
					},
				},
			},
		},
		{
			Name:        "netbox_get_sites",
			Description: "List sites from Netbox DCIM. Filter by name, status, region, or tenant.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Filter by site name (case-insensitive contains)",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status",
					},
					"region": map[string]interface{}{
						"type":        "string",
						"description": "Filter by region name (case-insensitive contains)",
					},
					"tenant": map[string]interface{}{
						"type":        "string",
						"description": "Filter by tenant name (case-insensitive contains)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results (default: 25)",
					},
				},
			},
		},
		{
			Name:        "netbox_get_vlans",
			Description: "List VLANs from Netbox. Filter by VID, name, site, status, or tenant.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"vid": map[string]interface{}{
						"type":        "integer",
						"description": "Filter by VLAN ID",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Filter by VLAN name (case-insensitive contains)",
					},
					"site": map[string]interface{}{
						"type":        "string",
						"description": "Filter by site name (case-insensitive contains)",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status",
					},
					"tenant": map[string]interface{}{
						"type":        "string",
						"description": "Filter by tenant name (case-insensitive contains)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results (default: 25)",
					},
				},
			},
		},
		{
			Name:        "netbox_get_interfaces",
			Description: "List interfaces from Netbox DCIM. Specify a device or VM name to filter.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device": map[string]interface{}{
						"type":        "string",
						"description": "Filter by device name (case-insensitive contains)",
					},
					"vm": map[string]interface{}{
						"type":        "string",
						"description": "Filter by virtual machine name (case-insensitive contains)",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Filter by interface name (case-insensitive contains)",
					},
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "Filter by enabled state",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results (default: 50)",
					},
				},
			},
		},
		{
			Name:        "netbox_introspect",
			Description: "Introspect the Netbox GraphQL schema. Optionally describe a specific type by name.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional: name of a specific GraphQL type to describe (e.g. DeviceType, IPAddress)",
					},
				},
			},
		},
	}, nil
}

// CallTool dispatches a tool call to the appropriate handler.
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

// Shutdown cleans up resources including the underlying graphql server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.gql.Shutdown(ctx)
}

// --- Tool handlers ---

func (s *Server) handleQuery(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return mcpserver.ErrorResult("query is required"), nil
	}
	vars, err := graphql.ParseVariables(args, "variables")
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("invalid variables: %v", err)), nil
	}
	return graphql.Execute(ctx, s.gql, query, vars, "")
}

func (s *Server) handleIntrospect(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	typeName, _ := args["type_name"].(string)
	if typeName != "" {
		return s.gql.CallTool(ctx, "graphql_describe_type", map[string]interface{}{"type_name": typeName})
	}
	return s.gql.CallTool(ctx, "graphql_introspect", map[string]interface{}{})
}

// --- Convenience tool handlers ---

func (s *Server) handleGetDevices(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	limit := toInt(args["limit"], 25)

	var filters []string
	if v, _ := args["name"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("name: {i_contains: %q}", v))
	}
	if v, _ := args["site"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("site: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["rack"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("rack: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["role"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("device_role: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["status"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("status: [{value: %q}]", v))
	}
	if v, _ := args["tenant"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("tenant: {name: {i_contains: %q}}", v))
	}

	query := fmt.Sprintf(`{
  device_list(%slimit: %d) {
    id
    name
    status { value label }
    site { name }
    rack { name }
    device_role { name }
    device_type { model manufacturer { name } }
    primary_ip4 { address }
    tenant { name }
  }
}`, buildFilters(filters), limit)

	resp, err := graphql.DoRequest(ctx, s.gql, query, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}

	// Parse and format response.
	type statusVal struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	type nameVal struct {
		Name string `json:"name"`
	}
	type manufacturerVal struct {
		Name string `json:"name"`
	}
	type deviceTypeVal struct {
		Model        string          `json:"model"`
		Manufacturer manufacturerVal `json:"manufacturer"`
	}
	type ipVal struct {
		Address string `json:"address"`
	}
	type device struct {
		ID         json.Number   `json:"id"`
		Name       string        `json:"name"`
		Status     statusVal     `json:"status"`
		Site       *nameVal      `json:"site"`
		Rack       *nameVal      `json:"rack"`
		DeviceRole *nameVal      `json:"device_role"`
		DeviceType *deviceTypeVal `json:"device_type"`
		PrimaryIP4 *ipVal        `json:"primary_ip4"`
		Tenant     *nameVal      `json:"tenant"`
	}
	type respData struct {
		DeviceList []device `json:"device_list"`
	}

	var data respData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse response: %v", err)), nil
	}

	if len(data.DeviceList) == 0 {
		return mcpserver.TextResult("No devices found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d device(s):\n\n", len(data.DeviceList))
	for _, d := range data.DeviceList {
		siteName := "-"
		if d.Site != nil {
			siteName = d.Site.Name
		}
		rackName := "-"
		if d.Rack != nil {
			rackName = d.Rack.Name
		}
		roleName := "-"
		if d.DeviceRole != nil {
			roleName = d.DeviceRole.Name
		}
		model := "-"
		if d.DeviceType != nil {
			model = d.DeviceType.Manufacturer.Name + " " + d.DeviceType.Model
		}
		ip := "-"
		if d.PrimaryIP4 != nil {
			ip = d.PrimaryIP4.Address
		}
		tenant := "-"
		if d.Tenant != nil {
			tenant = d.Tenant.Name
		}
		statusLabel := d.Status.Label
		if statusLabel == "" {
			statusLabel = d.Status.Value
		}

		fmt.Fprintf(&sb, "[%s] %s (%s)\n", d.ID, d.Name, statusLabel)
		fmt.Fprintf(&sb, "  Site: %s | Rack: %s | Role: %s\n", siteName, rackName, roleName)
		fmt.Fprintf(&sb, "  Model: %s\n", model)
		fmt.Fprintf(&sb, "  IP: %s\n", ip)
		fmt.Fprintf(&sb, "  Tenant: %s\n\n", tenant)
	}
	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetIPAddresses(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	limit := toInt(args["limit"], 25)

	var filters []string
	if v, _ := args["address"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("address: {i_contains: %q}", v))
	}
	if v, _ := args["status"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("status: [{value: %q}]", v))
	}
	if v, _ := args["device"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("device: {name: {i_contains: %q}}", v))
	}

	query := fmt.Sprintf(`{
  ip_address_list(%slimit: %d) {
    id
    address
    status { value }
    dns_name
    assigned_object {
      ... on InterfaceType { name device { name } }
    }
    tenant { name }
    vrf { name }
  }
}`, buildFilters(filters), limit)

	resp, err := graphql.DoRequest(ctx, s.gql, query, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}

	type statusVal struct {
		Value string `json:"value"`
	}
	type nameVal struct {
		Name string `json:"name"`
	}
	type interfaceObj struct {
		Name   string   `json:"name"`
		Device *nameVal `json:"device"`
	}
	type ipAddr struct {
		ID             json.Number   `json:"id"`
		Address        string        `json:"address"`
		Status         statusVal     `json:"status"`
		DNSName        string        `json:"dns_name"`
		AssignedObject *interfaceObj `json:"assigned_object"`
		Tenant         *nameVal      `json:"tenant"`
		VRF            *nameVal      `json:"vrf"`
	}
	type respData struct {
		IPAddressList []ipAddr `json:"ip_address_list"`
	}

	var data respData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse response: %v", err)), nil
	}

	if len(data.IPAddressList) == 0 {
		return mcpserver.TextResult("No IP addresses found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d IP address(es):\n\n", len(data.IPAddressList))
	for _, ip := range data.IPAddressList {
		assignment := "unassigned"
		if ip.AssignedObject != nil {
			if ip.AssignedObject.Device != nil {
				assignment = ip.AssignedObject.Device.Name + "/" + ip.AssignedObject.Name
			} else {
				assignment = ip.AssignedObject.Name
			}
		}
		extra := ""
		if ip.DNSName != "" {
			extra += " dns:" + ip.DNSName
		}
		if ip.VRF != nil {
			extra += " vrf:" + ip.VRF.Name
		}
		fmt.Fprintf(&sb, "[%s] %s (%s) → %s%s\n", ip.ID, ip.Address, ip.Status.Value, assignment, extra)
	}
	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetPrefixes(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	limit := toInt(args["limit"], 25)

	var filters []string
	if v, _ := args["prefix"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("prefix: {i_contains: %q}", v))
	}
	if v, _ := args["status"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("status: [{value: %q}]", v))
	}
	if v, _ := args["vrf"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("vrf: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["site"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("site: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["tenant"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("tenant: {name: {i_contains: %q}}", v))
	}

	query := fmt.Sprintf(`{
  prefix_list(%slimit: %d) {
    id
    prefix
    status { value }
    description
    vrf { name }
    site { name }
    tenant { name }
    vlan { vid name }
  }
}`, buildFilters(filters), limit)

	resp, err := graphql.DoRequest(ctx, s.gql, query, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}

	type statusVal struct {
		Value string `json:"value"`
	}
	type nameVal struct {
		Name string `json:"name"`
	}
	type vlanVal struct {
		VID  int    `json:"vid"`
		Name string `json:"name"`
	}
	type prefix struct {
		ID          json.Number `json:"id"`
		Prefix      string      `json:"prefix"`
		Status      statusVal   `json:"status"`
		Description string      `json:"description"`
		VRF         *nameVal    `json:"vrf"`
		Site        *nameVal    `json:"site"`
		Tenant      *nameVal    `json:"tenant"`
		VLAN        *vlanVal    `json:"vlan"`
	}
	type respData struct {
		PrefixList []prefix `json:"prefix_list"`
	}

	var data respData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse response: %v", err)), nil
	}

	if len(data.PrefixList) == 0 {
		return mcpserver.TextResult("No prefixes found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d prefix(es):\n\n", len(data.PrefixList))
	for _, p := range data.PrefixList {
		desc := p.Description
		if desc == "" {
			desc = "-"
		}
		line := fmt.Sprintf("[%s] %s (%s) — %s", p.ID, p.Prefix, p.Status.Value, desc)
		extra := []string{}
		if p.VRF != nil {
			extra = append(extra, "vrf:"+p.VRF.Name)
		}
		if p.Site != nil {
			extra = append(extra, "site:"+p.Site.Name)
		}
		if p.Tenant != nil {
			extra = append(extra, "tenant:"+p.Tenant.Name)
		}
		if p.VLAN != nil {
			extra = append(extra, fmt.Sprintf("vlan:%d(%s)", p.VLAN.VID, p.VLAN.Name))
		}
		if len(extra) > 0 {
			line += "\n  " + strings.Join(extra, " | ")
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetSites(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	limit := toInt(args["limit"], 25)

	var filters []string
	if v, _ := args["name"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("name: {i_contains: %q}", v))
	}
	if v, _ := args["status"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("status: [{value: %q}]", v))
	}
	if v, _ := args["region"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("region: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["tenant"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("tenant: {name: {i_contains: %q}}", v))
	}

	query := fmt.Sprintf(`{
  site_list(%slimit: %d) {
    id
    name
    slug
    status { value }
    description
    region { name }
    tenant { name }
    physical_address
  }
}`, buildFilters(filters), limit)

	resp, err := graphql.DoRequest(ctx, s.gql, query, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}

	type statusVal struct {
		Value string `json:"value"`
	}
	type nameVal struct {
		Name string `json:"name"`
	}
	type site struct {
		ID              json.Number `json:"id"`
		Name            string      `json:"name"`
		Slug            string      `json:"slug"`
		Status          statusVal   `json:"status"`
		Description     string      `json:"description"`
		Region          *nameVal    `json:"region"`
		Tenant          *nameVal    `json:"tenant"`
		PhysicalAddress string      `json:"physical_address"`
	}
	type respData struct {
		SiteList []site `json:"site_list"`
	}

	var data respData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse response: %v", err)), nil
	}

	if len(data.SiteList) == 0 {
		return mcpserver.TextResult("No sites found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d site(s):\n\n", len(data.SiteList))
	for _, site := range data.SiteList {
		desc := site.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(&sb, "[%s] %s (%s) — %s\n", site.ID, site.Name, site.Status.Value, desc)
		regionName := "-"
		if site.Region != nil {
			regionName = site.Region.Name
		}
		tenantName := "-"
		if site.Tenant != nil {
			tenantName = site.Tenant.Name
		}
		fmt.Fprintf(&sb, "  Region: %s | Tenant: %s | Slug: %s\n", regionName, tenantName, site.Slug)
		if site.PhysicalAddress != "" {
			fmt.Fprintf(&sb, "  Address: %s\n", site.PhysicalAddress)
		}
		sb.WriteString("\n")
	}
	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetVLANs(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	limit := toInt(args["limit"], 25)

	var filters []string
	if v := toInt(args["vid"], 0); v != 0 {
		filters = append(filters, fmt.Sprintf("vid: {exact: %d}", v))
	}
	if v, _ := args["name"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("name: {i_contains: %q}", v))
	}
	if v, _ := args["site"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("site: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["status"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("status: [{value: %q}]", v))
	}
	if v, _ := args["tenant"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("tenant: {name: {i_contains: %q}}", v))
	}

	query := fmt.Sprintf(`{
  vlan_list(%slimit: %d) {
    id
    vid
    name
    status { value }
    description
    site { name }
    tenant { name }
  }
}`, buildFilters(filters), limit)

	resp, err := graphql.DoRequest(ctx, s.gql, query, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}

	type statusVal struct {
		Value string `json:"value"`
	}
	type nameVal struct {
		Name string `json:"name"`
	}
	type vlan struct {
		ID          json.Number `json:"id"`
		VID         int         `json:"vid"`
		Name        string      `json:"name"`
		Status      statusVal   `json:"status"`
		Description string      `json:"description"`
		Site        *nameVal    `json:"site"`
		Tenant      *nameVal    `json:"tenant"`
	}
	type respData struct {
		VLANList []vlan `json:"vlan_list"`
	}

	var data respData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse response: %v", err)), nil
	}

	if len(data.VLANList) == 0 {
		return mcpserver.TextResult("No VLANs found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d VLAN(s):\n\n", len(data.VLANList))
	for _, v := range data.VLANList {
		desc := v.Description
		if desc == "" {
			desc = "-"
		}
		siteName := "-"
		if v.Site != nil {
			siteName = v.Site.Name
		}
		tenantName := "-"
		if v.Tenant != nil {
			tenantName = v.Tenant.Name
		}
		fmt.Fprintf(&sb, "[%s] VID %d — %s (%s)\n", v.ID, v.VID, v.Name, v.Status.Value)
		fmt.Fprintf(&sb, "  Site: %s | Tenant: %s | Desc: %s\n\n", siteName, tenantName, desc)
	}
	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetInterfaces(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	limit := toInt(args["limit"], 50)

	var filters []string
	if v, _ := args["device"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("device: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["vm"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("virtual_machine: {name: {i_contains: %q}}", v))
	}
	if v, _ := args["name"].(string); v != "" {
		filters = append(filters, fmt.Sprintf("name: {i_contains: %q}", v))
	}
	if v, ok := args["enabled"].(bool); ok {
		if v {
			filters = append(filters, "enabled: true")
		} else {
			filters = append(filters, "enabled: false")
		}
	}

	query := fmt.Sprintf(`{
  interface_list(%slimit: %d) {
    id
    name
    type { value label }
    enabled
    mtu
    mac_address
    description
    device { name }
    ip_addresses { address }
  }
}`, buildFilters(filters), limit)

	resp, err := graphql.DoRequest(ctx, s.gql, query, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}

	type typeVal struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	type nameVal struct {
		Name string `json:"name"`
	}
	type ipAddr struct {
		Address string `json:"address"`
	}
	type iface struct {
		ID          json.Number `json:"id"`
		Name        string      `json:"name"`
		Type        typeVal     `json:"type"`
		Enabled     bool        `json:"enabled"`
		MTU         *int        `json:"mtu"`
		MACAddress  string      `json:"mac_address"`
		Description string      `json:"description"`
		Device      *nameVal    `json:"device"`
		IPAddresses []ipAddr    `json:"ip_addresses"`
	}
	type respData struct {
		InterfaceList []iface `json:"interface_list"`
	}

	var data respData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse response: %v", err)), nil
	}

	if len(data.InterfaceList) == 0 {
		return mcpserver.TextResult("No interfaces found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d interface(s):\n\n", len(data.InterfaceList))
	for _, iface := range data.InterfaceList {
		deviceName := "-"
		if iface.Device != nil {
			deviceName = iface.Device.Name
		}
		typeLabel := iface.Type.Label
		if typeLabel == "" {
			typeLabel = iface.Type.Value
		}
		enabledStr := "disabled"
		if iface.Enabled {
			enabledStr = "enabled"
		}
		fmt.Fprintf(&sb, "[%s] %s/%s (%s, %s)\n", iface.ID, deviceName, iface.Name, typeLabel, enabledStr)
		if iface.MACAddress != "" {
			fmt.Fprintf(&sb, "  MAC: %s\n", iface.MACAddress)
		}
		if iface.MTU != nil {
			fmt.Fprintf(&sb, "  MTU: %d\n", *iface.MTU)
		}
		if len(iface.IPAddresses) > 0 {
			addrs := make([]string, len(iface.IPAddresses))
			for i, ip := range iface.IPAddresses {
				addrs[i] = ip.Address
			}
			fmt.Fprintf(&sb, "  IPs: %s\n", strings.Join(addrs, ", "))
		}
		if iface.Description != "" {
			fmt.Fprintf(&sb, "  Desc: %s\n", iface.Description)
		}
		sb.WriteString("\n")
	}
	return mcpserver.TextResult(sb.String()), nil
}

// --- helpers ---

// buildFilters assembles the filters argument for a GraphQL list query.
// If filters is empty it returns an empty string. Otherwise it returns
// "filters: { ... }, " (note the trailing comma+space for use before limit:).
func buildFilters(filters []string) string {
	if len(filters) == 0 {
		return ""
	}
	return "filters: {" + strings.Join(filters, ", ") + "}, "
}

func formatErrors(errs []graphql.GQLError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message
	}
	return strings.Join(msgs, "; ")
}

// toInt converts a JSON-decoded number (float64) or int to int.
// Returns def if the value is absent or zero.
func toInt(v interface{}, def int) int {
	switch n := v.(type) {
	case float64:
		if int(n) != 0 {
			return int(n)
		}
	case int:
		if n != 0 {
			return n
		}
	case int64:
		if int(n) != 0 {
			return int(n)
		}
	}
	return def
}


