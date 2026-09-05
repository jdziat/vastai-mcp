// Package tools registers the Vast.ai MCP tools.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdziat/vastai-mcp/internal/vast"
)

type deps struct {
	c      *vast.Client
	cfg    Config
	audit  *auditor
	signer *stateSigner
}

// ReadOnlyTools and MutatingTools list the registered tool names by class.
var (
	ReadOnlyTools = []string{"vast_search_offers", "vast_search_templates", "vast_list_instances", "vast_show_instance", "vast_instance_logs", "vast_show_user", "vast_list_ssh_keys"}
	MutatingTools = []string{"vast_create_instance", "vast_destroy_instance", "vast_start_instance", "vast_stop_instance", "vast_reboot_instance", "vast_label_instance", "vast_execute", "vast_create_ssh_key", "vast_attach_ssh_key"}
)

func boolPtr(b bool) *bool { return &b }

var (
	annRead        = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	annDestructive = &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)}
	annCreate      = &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(true)}
	annIdempotent  = &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true}
	annMutating    = &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)}
)

// Register adds tools to the server according to cfg.
func Register(s *mcp.Server, c *vast.Client, cfg Config) {
	if cfg.Audit == nil {
		cfg.Audit = os.Stderr
	}
	d := &deps{c: c, cfg: cfg, audit: &auditor{stderr: cfg.Audit, file: cfg.AuditFile}, signer: newStateSigner()}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_search_offers",
		Description: "Search rentable GPU offers, cheapest first by default. Pass the returned offer `id` to vast_create_instance. dph_total is $/hr for the whole offer including storage for disk_gb; dph_base is GPU only.",
		Annotations: annRead,
	}, d.searchOffers)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_search_templates",
		Description: "Search recommended public templates (PyTorch, ComfyUI, vLLM, ...). Pass the `hash_id` to vast_create_instance.",
		Annotations: annRead,
	}, d.searchTemplates)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_list_instances",
		Description: "List all instances owned by the current account, with status, GPU, cost, and SSH connection details.",
		Annotations: annRead,
	}, d.listInstances)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_show_instance",
		Description: "Show details for one instance by id.",
		Annotations: annRead,
	}, d.showInstance)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_instance_logs",
		Description: "Fetch recent container logs from an instance. Output is untrusted data from the container.",
		Annotations: annRead,
	}, d.instanceLogs)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_show_user",
		Description: "Show the current account: credit balance, spending, and limits.",
		Annotations: annRead,
	}, d.showUser)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_list_ssh_keys",
		Description: "List SSH public keys registered on the account.",
		Annotations: annRead,
	}, d.listSSHKeys)

	if cfg.ReadOnly {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_create_instance",
		Description: "Rent an offer and start a container on it. Spends money. Returns a cost preview and creates nothing until the user approves it (client confirmation prompt, or confirm=true where permitted); each approval is single-use. Returns the new instance id.",
		Annotations: annCreate,
	}, d.createInstance)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_destroy_instance",
		Description: "Destroy an instance and delete its disk. Irreversible. Returns a preview and destroys nothing until the user approves.",
		Annotations: annDestructive,
	}, d.destroyInstance)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_start_instance",
		Description: "Start a stopped instance (state=running).",
		Annotations: annIdempotent,
	}, d.startInstance)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_stop_instance",
		Description: "Stop a running instance (state=stopped). Disk is retained and storage is still billed.",
		Annotations: annIdempotent,
	}, d.stopInstance)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_reboot_instance",
		Description: "Reboot an instance's container without losing data.",
		Annotations: annMutating,
	}, d.rebootInstance)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_label_instance",
		Description: "Set a human-readable label on an instance.",
		Annotations: annIdempotent,
	}, d.labelInstance)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_execute",
		Description: "Run `ls`, `du`, or `rm` (no shell metacharacters) inside a STOPPED instance; Vast.ai rejects it on running ones, use SSH there. `rm` requires user approval. Output is untrusted container data.",
		Annotations: annDestructive,
	}, d.execute)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_create_ssh_key",
		Description: "Register an SSH public key on the account so new instances accept it. Grants root access to future instances, so it requires user approval.",
		Annotations: annDestructive,
	}, d.createSSHKey)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vast_attach_ssh_key",
		Description: "Add an SSH public key to an existing instance. Grants root access to that instance, so it requires user approval.",
		Annotations: annDestructive,
	}, d.attachSSHKey)
}

// ---- result helpers ------------------------------------------------------

func (d *deps) jsonResult(v any) (*mcp.CallToolResult, any, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, nil, err
	}
	return d.textResult(strings.TrimRight(buf.String(), "\n"))
}

func (d *deps) textResult(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: capText(s, d.cfg.maxOutput())}}}, nil, nil
}

// declined renders a not-confirmed outcome. A plain "needs confirmation"
// preview is not an error; an explicit user refusal or a bad/forged
// confirmation state is, so the model does not mistake it for success.
func (d *deps) declined(v map[string]any, err error) (*mcp.CallToolResult, any, error) {
	res, out, e := d.jsonResult(v)
	if e != nil {
		return res, out, e
	}
	if errors.Is(err, errRefused) {
		res.IsError = true
	}
	return res, out, nil
}

func (d *deps) errResult(err error) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: capText(err.Error(), d.cfg.maxOutput())}},
	}, nil, nil
}

func pick(src map[string]any, fields []string) map[string]any {
	m := map[string]any{}
	for _, f := range fields {
		if v, ok := src[f]; ok && v != nil {
			m[f] = v
		}
	}
	return m
}

// ---- search offers ------------------------------------------------------

type SearchOffersArgs struct {
	GPUName        string  `json:"gpu_name,omitempty" jsonschema:"GPU model as shown by Vast.ai, e.g. 'RTX 4090', 'A100 SXM4', 'H100 SXM'"`
	NumGPUs        int     `json:"num_gpus,omitempty" jsonschema:"Exact number of GPUs per offer"`
	MinGPURAM      float64 `json:"min_gpu_ram_gb,omitempty" jsonschema:"Minimum VRAM per GPU in GB"`
	MinDiskGB      float64 `json:"min_disk_gb,omitempty" jsonschema:"Minimum available disk in GB"`
	DiskGB         float64 `json:"disk_gb,omitempty" jsonschema:"Disk you intend to allocate in GB, for the quoted storage cost (default 10)"`
	MinCPURAM      float64 `json:"min_cpu_ram_gb,omitempty" jsonschema:"Minimum system RAM in GB"`
	MaxPrice       float64 `json:"max_dph,omitempty" jsonschema:"Maximum price in $/hour"`
	MinReliability float64 `json:"min_reliability,omitempty" jsonschema:"Minimum host reliability 0-1, e.g. 0.95"`
	Interruptible  bool    `json:"interruptible,omitempty" jsonschema:"Search interruptible (bid) offers instead of on-demand"`
	Unverified     bool    `json:"include_unverified,omitempty" jsonschema:"Include unverified hosts"`
	OrderBy        string  `json:"order_by,omitempty" jsonschema:"Comma-separated sort fields, '-' prefix for descending. Default dph_total. e.g. -dlperf_per_dphtotal"`
	Limit          int     `json:"limit,omitempty" jsonschema:"Max results (default 20, max 100)"`
	RawQuery       string  `json:"raw_query,omitempty" jsonschema:"Raw Vast.ai JSON query merged over the filters above, for any other field: e.g. {\"geolocation\":{\"eq\":\"US\"},\"inet_down\":{\"gte\":500},\"static_ip\":{\"eq\":true},\"direct_port_count\":{\"gte\":4},\"cuda_max_good\":{\"gte\":12.4}}"`
}

const defaultDiskGB = 10.0

func buildOfferQuery(a SearchOffersArgs) (map[string]any, vast.SearchDefaults, error) {
	q := map[string]any{}
	var defs vast.SearchDefaults
	if a.GPUName != "" {
		q["gpu_name"] = map[string]any{"eq": strings.ReplaceAll(a.GPUName, "_", " ")}
	}
	if a.NumGPUs > 0 {
		q["num_gpus"] = map[string]any{"eq": a.NumGPUs}
	}
	if a.MinGPURAM > 0 {
		q["gpu_ram"] = map[string]any{"gte": a.MinGPURAM * 1024} // API reports MB
	}
	if a.MinDiskGB > 0 {
		q["disk_space"] = map[string]any{"gte": a.MinDiskGB}
	}
	if a.MinCPURAM > 0 {
		q["cpu_ram"] = map[string]any{"gte": a.MinCPURAM * 1024} // API reports MB
	}
	if a.MaxPrice > 0 {
		q["dph_total"] = map[string]any{"lte": a.MaxPrice}
	}
	if a.MinReliability > 0 {
		q["reliability2"] = map[string]any{"gte": a.MinReliability}
	}
	disk := a.DiskGB
	if disk <= 0 {
		disk = defaultDiskGB
	}
	q["allocated_storage"] = disk
	if a.Interruptible {
		q["type"] = "bid"
		defs.SkipRented = true
	}
	if a.Unverified {
		defs.SkipVerified = true
	}
	if a.RawQuery != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(a.RawQuery), &extra); err != nil {
			return nil, defs, fmt.Errorf("raw_query is not valid JSON: %w", err)
		}
		for k, v := range extra {
			q[k] = v
		}
	}
	return q, defs, nil
}

func (d *deps) searchOffers(ctx context.Context, _ *mcp.CallToolRequest, a SearchOffersArgs) (*mcp.CallToolResult, any, error) {
	q, defs, err := buildOfferQuery(a)
	if err != nil {
		return d.errResult(err)
	}
	order := a.OrderBy
	if order == "" {
		order = "dph_total"
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offers, err := d.c.SearchOffers(ctx, q, defs, order, limit)
	if err != nil {
		return d.errResult(err)
	}
	return d.jsonResult(map[string]any{"query": q, "offers": summarizeOffers(offers, limit)})
}

var offerFields = []string{
	"id", "gpu_name", "num_gpus", "gpu_ram", "gpu_total_ram", "cpu_name", "cpu_cores_effective", "cpu_ram",
	"disk_space", "disk_bw", "dph_total", "dph_base", "storage_cost", "inet_up", "inet_down", "inet_up_cost", "inet_down_cost",
	"reliability2", "dlperf", "dlperf_per_dphtotal", "cuda_max_good", "driver_version", "geolocation",
	"static_ip", "direct_port_count", "verification", "rentable", "rented", "machine_id", "host_id", "min_bid", "is_bid", "duration",
}

func summarizeOffers(offers []map[string]any, limit int) []map[string]any {
	out := make([]map[string]any, 0, len(offers))
	for _, o := range offers {
		out = append(out, pick(o, offerFields))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// ---- instances ----------------------------------------------------------

type IDArgs struct {
	ID int64 `json:"id" jsonschema:"Instance id"`
}

var instanceFields = []string{
	"id", "label", "actual_status", "cur_state", "intended_status", "status_msg", "gpu_name", "num_gpus", "gpu_ram", "gpu_util",
	"cpu_name", "cpu_cores_effective", "cpu_ram", "disk_space", "disk_usage", "image_uuid", "image_runtype", "onstart",
	"dph_total", "storage_cost", "inet_up_cost", "inet_down_cost", "ssh_host", "ssh_port", "public_ipaddr", "ports", "jupyter_token",
	"machine_id", "host_id", "geolocation", "reliability2", "start_date", "end_date", "duration", "is_bid", "min_bid", "cuda_max_good", "driver_version",
	"template_hash_id", "template_name",
}

func (d *deps) summarizeInstance(o map[string]any) map[string]any {
	m := pick(o, instanceFields)
	if h, ok := o["ssh_host"]; ok && h != nil {
		if p, ok := o["ssh_port"]; ok && p != nil {
			m["ssh_command"] = fmt.Sprintf("ssh -p %v root@%v", p, h)
		}
	}
	return d.redactMap(m)
}

func (d *deps) listInstances(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	list, err := d.c.ListInstances(ctx)
	if err != nil {
		return d.errResult(err)
	}
	out := make([]map[string]any, 0, len(list))
	for _, o := range list {
		out = append(out, d.summarizeInstance(o))
	}
	return d.jsonResult(out)
}

type ShowInstanceArgs struct {
	ID  int64 `json:"id" jsonschema:"Instance id"`
	Raw bool  `json:"raw,omitempty" jsonschema:"Return every field the API provides instead of the summary"`
}

func (d *deps) showInstance(ctx context.Context, _ *mcp.CallToolRequest, a ShowInstanceArgs) (*mcp.CallToolResult, any, error) {
	inst, err := d.c.ShowInstance(ctx, a.ID)
	if err != nil {
		return d.errResult(err)
	}
	if inst == nil {
		return d.errResult(fmt.Errorf("instance %d not found", a.ID))
	}
	if a.Raw {
		return d.jsonResult(d.redactMap(inst))
	}
	return d.jsonResult(d.summarizeInstance(inst))
}

type CreateInstanceArgs struct {
	OfferID        int64             `json:"offer_id" jsonschema:"Offer id from vast_search_offers"`
	Image          string            `json:"image,omitempty" jsonschema:"Docker image, e.g. pytorch/pytorch:2.4.0-cuda12.4-cudnn9-runtime. Required unless template_hash_id is set."`
	TemplateHashID string            `json:"template_hash_id,omitempty" jsonschema:"Template hash_id from vast_search_templates (sets image/onstart/env/ports)"`
	DiskGB         float64           `json:"disk_gb,omitempty" jsonschema:"Container disk size in GB (default 10)"`
	Label          string            `json:"label,omitempty" jsonschema:"Label for the instance"`
	OnStart        string            `json:"onstart,omitempty" jsonschema:"Shell script to run on start"`
	RunType        string            `json:"runtype,omitempty" jsonschema:"ssh (default), jupyter, args, ssh_direc, ssh_proxy, jupyter_direc, jupyter_proxy"`
	Env            map[string]string `json:"env,omitempty" jsonschema:"Environment variables. Keys starting with '-p ' declare port mappings, e.g. \"-p 8080:8080\": \"1\""`
	ImageLogin     string            `json:"image_login,omitempty" jsonschema:"Registry login string for private images: '-u USER -p PASS registry'"`
	BidPrice       float64           `json:"bid_price,omitempty" jsonschema:"$/hr bid for interruptible offers (omit for on-demand)"`
	CancelUnavail  bool              `json:"cancel_unavail,omitempty" jsonschema:"Cancel automatically if the offer is no longer available"`
	Confirm        bool              `json:"confirm,omitempty" jsonschema:"Set true only after the user has approved the preview returned by a previous call"`
}

// num extracts a numeric field; ok is false when absent or not a number.
func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func (d *deps) createInstance(ctx context.Context, req *mcp.CallToolRequest, a CreateInstanceArgs) (*mcp.CallToolResult, any, error) {
	const tool = "vast_create_instance"
	if a.OfferID == 0 {
		return d.errResult(errors.New("offer_id is required"))
	}
	if a.Image == "" && a.TemplateHashID == "" {
		return d.errResult(errors.New("image or template_hash_id is required"))
	}
	disk := a.DiskGB
	if disk <= 0 {
		disk = defaultDiskGB
	}

	// Resolve the offer with no default filters so unverified/bid/rented ids
	// resolve, priced for the disk we intend to allocate.
	offer, err := d.c.LookupOffer(ctx, a.OfferID, a.BidPrice > 0, disk)
	if err != nil {
		return d.errResult(fmt.Errorf("lookup offer %d: %w", a.OfferID, err))
	}
	if offer == nil {
		d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": "offer not found"})
		return d.errResult(fmt.Errorf("offer %d not found (it may have been rented or withdrawn); search again", a.OfferID))
	}
	// dph_total from the lookup already includes storage for `disk`.
	total, priceKnown := num(offer["dph_total"])
	gpuHourly, _ := num(offer["dph_base"])
	storageHourly, _ := num(offer["storage_total_cost"])
	if a.BidPrice > 0 {
		if mb, ok := num(offer["min_bid"]); ok && a.BidPrice < mb {
			return d.errResult(fmt.Errorf("bid_price %.4f is below the offer's min_bid %.4f", a.BidPrice, mb))
		}
		gpuHourly = a.BidPrice
		total, priceKnown = a.BidPrice+storageHourly, true
	}
	if d.cfg.MaxDPH > 0 {
		if !priceKnown {
			d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": "price unknown"})
			return d.errResult(fmt.Errorf("offer %d has no usable dph_total; refusing to create while -max-dph is set", a.OfferID))
		}
		if total > d.cfg.MaxDPH {
			d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": "max_dph", "total_usd_hr": total, "cap": d.cfg.MaxDPH})
			return d.errResult(fmt.Errorf("offer costs $%.4f/hr including $%.4f/hr storage, which exceeds the -max-dph cap of $%.4f/hr", total, storageHourly, d.cfg.MaxDPH))
		}
	}
	if d.cfg.MaxInstances > 0 {
		list, err := d.c.ListInstances(ctx)
		if err != nil {
			return d.errResult(fmt.Errorf("count instances: %w", err))
		}
		if len(list) >= d.cfg.MaxInstances {
			d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": "max_instances", "count": len(list), "cap": d.cfg.MaxInstances})
			return d.errResult(fmt.Errorf("%d instances already exist; -max-instances cap is %d", len(list), d.cfg.MaxInstances))
		}
	}

	preview := map[string]any{
		"offer":                  pick(offer, []string{"id", "gpu_name", "num_gpus", "gpu_ram", "disk_space", "dph_base", "dph_total", "storage_cost", "verification", "geolocation", "reliability2"}),
		"image":                  a.Image,
		"template_hash_id":       a.TemplateHashID,
		"disk_gb":                disk,
		"label":                  a.Label,
		"estimated_gpu_usd_hr":   round4(gpuHourly),
		"estimated_disk_usd_hr":  round4(storageHourly),
		"estimated_total_usd_hr": round4(total),
		"estimated_usd_per_day":  round4(total * 24),
	}
	pb, _ := json.MarshalIndent(preview, "", "  ")
	if ask, err := d.confirm(req, a.Confirm, tool, "creating this instance", string(pb), total); ask != nil || err != nil {
		if ask != nil {
			return ask, nil, nil
		}
		d.audit.log(tool, req.Params.Arguments, "not_confirmed", map[string]any{"reason": err.Error()})
		if errors.Is(err, ErrNotConfirmed) {
			return d.declined(map[string]any{"status": "not_created", "reason": err.Error(), "preview": preview}, err)
		}
		return d.errResult(err)
	}

	if d.cfg.MaxInstances > 0 {
		// Re-check after approval: two concurrently approved creates must not both land.
		list, err := d.c.ListInstances(ctx)
		if err != nil {
			return d.errResult(fmt.Errorf("count instances: %w", err))
		}
		if len(list) >= d.cfg.MaxInstances {
			d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": "max_instances", "count": len(list), "cap": d.cfg.MaxInstances})
			return d.errResult(fmt.Errorf("%d instances already exist; -max-instances cap is %d", len(list), d.cfg.MaxInstances))
		}
	}
	p := vast.CreateInstanceParams{
		Image: a.Image, Disk: disk, Label: a.Label, OnStart: a.OnStart, RunType: a.RunType,
		Env: a.Env, ImageLogin: a.ImageLogin, Price: a.BidPrice, TemplateHashID: a.TemplateHashID, CancelUnavail: a.CancelUnavail,
	}
	if p.RunType == "" && p.TemplateHashID == "" {
		p.RunType = "ssh"
	}
	res, err := d.c.CreateInstance(ctx, a.OfferID, p)
	if err != nil {
		d.audit.log(tool, req.Params.Arguments, "error", map[string]any{"error": err.Error()})
		return d.errResult(err)
	}
	d.audit.log(tool, req.Params.Arguments, "created", map[string]any{"instance_id": res.NewContract, "estimated_usd_hr": total})

	out := map[string]any{"status": "created", "instance_id": res.NewContract, "preview": preview}
	// Post-create TOCTOU check: the offer may have repriced between lookup and PUT.
	if d.cfg.MaxDPH > 0 && res.NewContract > 0 {
		if inst, err := d.c.ShowInstance(ctx, res.NewContract); err == nil && inst != nil {
			actual, _ := num(inst["dph_total"]) // instance dph_total includes its storage
			out["actual_usd_hr"] = round4(actual)
			if actual > d.cfg.MaxDPH {
				d.audit.log(tool, req.Params.Arguments, "PRICE_BREACH", map[string]any{"instance_id": res.NewContract, "actual_usd_hr": actual, "cap": d.cfg.MaxDPH})
				out["WARNING"] = fmt.Sprintf("PRICE BREACH: instance %d is billing $%.4f/hr, above the -max-dph cap of $%.4f/hr. Consider destroying it.", res.NewContract, actual, d.cfg.MaxDPH)
			}
		}
	}
	return d.jsonResult(out)
}

func round4(f float64) float64 { return float64(int64(f*10000+0.5)) / 10000 }

type DestroyArgs struct {
	ID      int64 `json:"id" jsonschema:"Instance id"`
	Confirm bool  `json:"confirm,omitempty" jsonschema:"Set true only after the user has approved the preview returned by a previous call"`
}

func (d *deps) destroyInstance(ctx context.Context, req *mcp.CallToolRequest, a DestroyArgs) (*mcp.CallToolResult, any, error) {
	const tool = "vast_destroy_instance"
	inst, err := d.c.ShowInstance(ctx, a.ID)
	if err != nil {
		return d.errResult(err)
	}
	if inst == nil {
		return d.errResult(fmt.Errorf("instance %d not found", a.ID))
	}
	preview := pick(inst, []string{"id", "label", "actual_status", "gpu_name", "num_gpus", "image_uuid", "dph_total", "disk_space", "start_date"})
	pb, _ := json.MarshalIndent(preview, "", "  ")
	if ask, err := d.confirm(req, a.Confirm, tool, fmt.Sprintf("DESTROYING instance %d (irreversible, deletes its disk)", a.ID), string(pb), 0); ask != nil || err != nil {
		if ask != nil {
			return ask, nil, nil
		}
		d.audit.log(tool, req.Params.Arguments, "not_confirmed", map[string]any{"reason": err.Error()})
		if errors.Is(err, ErrNotConfirmed) {
			return d.declined(map[string]any{"status": "not_destroyed", "reason": err.Error(), "instance": preview}, err)
		}
		return d.errResult(err)
	}
	res, err := d.c.DestroyInstance(ctx, a.ID)
	if err != nil {
		d.audit.log(tool, req.Params.Arguments, "error", map[string]any{"error": err.Error()})
		return d.errResult(err)
	}
	d.audit.log(tool, req.Params.Arguments, "destroyed", map[string]any{"instance_id": a.ID})
	return d.jsonResult(map[string]any{"status": "destroyed", "instance": preview, "response": res})
}

func (d *deps) simpleMutation(ctx context.Context, req *mcp.CallToolRequest, tool string, fn func() (any, error)) (*mcp.CallToolResult, any, error) {
	res, err := fn()
	if err != nil {
		d.audit.log(tool, req.Params.Arguments, "error", map[string]any{"error": err.Error()})
		return d.errResult(err)
	}
	d.audit.log(tool, req.Params.Arguments, "ok", nil)
	return d.jsonResult(res)
}

func (d *deps) startInstance(ctx context.Context, req *mcp.CallToolRequest, a IDArgs) (*mcp.CallToolResult, any, error) {
	const tool = "vast_start_instance"
	if d.cfg.MaxDPH > 0 {
		// Starting resumes billing, so the spend cap applies here too.
		inst, err := d.c.ShowInstance(ctx, a.ID)
		if err != nil {
			return d.errResult(err)
		}
		if inst == nil {
			return d.errResult(fmt.Errorf("instance %d not found", a.ID))
		}
		total, ok := num(inst["dph_total"]) // includes storage
		if !ok {
			d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": "price unknown"})
			return d.errResult(fmt.Errorf("instance %d has no usable dph_total; refusing to start while -max-dph is set", a.ID))
		}
		if total > d.cfg.MaxDPH {
			d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": "max_dph", "total_usd_hr": total, "cap": d.cfg.MaxDPH})
			return d.errResult(fmt.Errorf("instance %d bills $%.4f/hr when running, which exceeds the -max-dph cap of $%.4f/hr", a.ID, total, d.cfg.MaxDPH))
		}
	}
	return d.simpleMutation(ctx, req, tool, func() (any, error) { return d.c.SetInstanceState(ctx, a.ID, "running") })
}

func (d *deps) stopInstance(ctx context.Context, req *mcp.CallToolRequest, a IDArgs) (*mcp.CallToolResult, any, error) {
	return d.simpleMutation(ctx, req, "vast_stop_instance", func() (any, error) { return d.c.SetInstanceState(ctx, a.ID, "stopped") })
}

func (d *deps) rebootInstance(ctx context.Context, req *mcp.CallToolRequest, a IDArgs) (*mcp.CallToolResult, any, error) {
	return d.simpleMutation(ctx, req, "vast_reboot_instance", func() (any, error) { return d.c.RebootInstance(ctx, a.ID) })
}

type LabelArgs struct {
	ID    int64  `json:"id" jsonschema:"Instance id"`
	Label string `json:"label" jsonschema:"New label"`
}

func (d *deps) labelInstance(ctx context.Context, req *mcp.CallToolRequest, a LabelArgs) (*mcp.CallToolResult, any, error) {
	return d.simpleMutation(ctx, req, "vast_label_instance", func() (any, error) { return d.c.LabelInstance(ctx, a.ID, a.Label) })
}

type LogsArgs struct {
	ID     int64  `json:"id" jsonschema:"Instance id"`
	Tail   int    `json:"tail,omitempty" jsonschema:"Number of trailing lines (default 1000)"`
	Filter string `json:"filter,omitempty" jsonschema:"grep-style filter applied server-side"`
}

func (d *deps) instanceLogs(ctx context.Context, _ *mcp.CallToolRequest, a LogsArgs) (*mcp.CallToolResult, any, error) {
	if a.Tail <= 0 {
		a.Tail = 1000
	}
	out, err := d.c.InstanceLogs(ctx, a.ID, a.Tail, a.Filter)
	if err != nil {
		return d.errResult(err)
	}
	return d.textResult(wrapUntrusted(fmt.Sprintf("instance %d logs", a.ID), out, d.cfg.maxOutput()))
}

type ExecArgs struct {
	ID      int64  `json:"id" jsonschema:"Instance id"`
	Command string `json:"command" jsonschema:"Command to run; only ls, du, rm are allowed, without shell metacharacters"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"Required for rm: set true only after the user has approved"`
}

func (d *deps) execute(ctx context.Context, req *mcp.CallToolRequest, a ExecArgs) (*mcp.CallToolResult, any, error) {
	const tool = "vast_execute"
	first, err := validateExecCommand(a.Command)
	if err != nil {
		d.audit.log(tool, req.Params.Arguments, "rejected", map[string]any{"reason": err.Error()})
		return d.errResult(err)
	}
	if first == "rm" {
		if ask, err := d.confirm(req, a.Confirm, tool, fmt.Sprintf("running `%s` on instance %d", a.Command, a.ID), "This deletes files inside the container.", 0); ask != nil || err != nil {
			if ask != nil {
				return ask, nil, nil
			}
			d.audit.log(tool, req.Params.Arguments, "not_confirmed", map[string]any{"reason": err.Error()})
			if errors.Is(err, ErrNotConfirmed) {
				return d.declined(map[string]any{"status": "not_run", "reason": err.Error(), "command": a.Command}, err)
			}
			return d.errResult(err)
		}
	}
	out, err := d.c.Execute(ctx, a.ID, a.Command)
	if err != nil {
		d.audit.log(tool, req.Params.Arguments, "error", map[string]any{"error": err.Error()})
		return d.errResult(err)
	}
	d.audit.log(tool, req.Params.Arguments, "ok", nil)
	return d.textResult(wrapUntrusted(fmt.Sprintf("instance %d command output", a.ID), out, d.cfg.maxOutput()))
}

// ---- account ------------------------------------------------------------

func (d *deps) showUser(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	u, err := d.c.ShowUser(ctx)
	if err != nil {
		return d.errResult(err)
	}
	// Always strip credentials regardless of exposure setting.
	for _, k := range []string{"api_key", "ssh_key", "crisp_hmac", "sid", "key_id"} {
		delete(u, k)
	}
	return d.jsonResult(d.redactMap(u))
}

func (d *deps) listSSHKeys(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	res, err := d.c.ListSSHKeys(ctx)
	if err != nil {
		return d.errResult(err)
	}
	if list, ok := res.([]any); ok {
		out := make([]map[string]any, 0, len(list))
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				out = append(out, pick(m, []string{"id", "public_key", "created_at", "default"}))
			}
		}
		return d.jsonResult(out)
	}
	return d.jsonResult(res)
}

type SSHKeyArgs struct {
	PublicKey string `json:"public_key" jsonschema:"SSH public key text (e.g. contents of ~/.ssh/id_ed25519.pub)"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"Set true only after the user has approved the preview returned by a previous call"`
}

func validatePublicKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	f := strings.Fields(s)
	if len(f) < 2 || !strings.HasPrefix(f[0], "ssh-") && !strings.HasPrefix(f[0], "ecdsa-") && !strings.HasPrefix(f[0], "sk-") {
		return "", errors.New("public_key does not look like an OpenSSH public key")
	}
	if strings.Contains(s, "PRIVATE KEY") {
		return "", errors.New("refusing to upload what looks like a private key")
	}
	return s, nil
}

// confirmKeyGrant gates SSH-key tools: a key is root access, and the key
// text may have arrived via untrusted container output.
func (d *deps) confirmKeyGrant(req *mcp.CallToolRequest, confirmArg bool, tool, action, key string) (*mcp.CallToolResult, any, error, bool) {
	preview := "public key: " + keyFingerprint(key)
	ask, err := d.confirm(req, confirmArg, tool, action, preview, 0)
	if ask != nil {
		return ask, nil, nil, true
	}
	if err != nil {
		d.audit.log(tool, req.Params.Arguments, "not_confirmed", map[string]any{"reason": err.Error()})
		if errors.Is(err, ErrNotConfirmed) {
			r, o, e := d.declined(map[string]any{"status": "not_added", "reason": err.Error(), "public_key": keyFingerprint(key)}, err)
			return r, o, e, true
		}
		r, o, e := d.errResult(err)
		return r, o, e, true
	}
	return nil, nil, nil, false
}

func (d *deps) createSSHKey(ctx context.Context, req *mcp.CallToolRequest, a SSHKeyArgs) (*mcp.CallToolResult, any, error) {
	key, err := validatePublicKey(a.PublicKey)
	if err != nil {
		return d.errResult(err)
	}
	if r, o, e, done := d.confirmKeyGrant(req, a.Confirm, "vast_create_ssh_key", "registering an SSH key on the account (root access to future instances)", key); done {
		return r, o, e
	}
	return d.simpleMutation(ctx, req, "vast_create_ssh_key", func() (any, error) { return d.c.CreateSSHKey(ctx, key) })
}

type AttachSSHKeyArgs struct {
	ID        int64  `json:"id" jsonschema:"Instance id"`
	PublicKey string `json:"public_key" jsonschema:"SSH public key text"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"Set true only after the user has approved the preview returned by a previous call"`
}

func (d *deps) attachSSHKey(ctx context.Context, req *mcp.CallToolRequest, a AttachSSHKeyArgs) (*mcp.CallToolResult, any, error) {
	key, err := validatePublicKey(a.PublicKey)
	if err != nil {
		return d.errResult(err)
	}
	if r, o, e, done := d.confirmKeyGrant(req, a.Confirm, "vast_attach_ssh_key", fmt.Sprintf("attaching an SSH key to instance %d (root access)", a.ID), key); done {
		return r, o, e
	}
	return d.simpleMutation(ctx, req, "vast_attach_ssh_key", func() (any, error) { return d.c.AttachSSHKey(ctx, a.ID, key) })
}

// ---- templates ----------------------------------------------------------

type SearchTemplatesArgs struct {
	Name        string `json:"name,omitempty" jsonschema:"Case-insensitive substring match on template name, image, or description, e.g. pytorch, comfyui, vllm"`
	Recommended *bool  `json:"recommended,omitempty" jsonschema:"Only Vast.ai recommended templates (default true). false searches all ~2000 public templates."`
	Limit       int    `json:"limit,omitempty" jsonschema:"Max results (default 20)"`
	RawFilters  string `json:"raw_filters,omitempty" jsonschema:"Optional raw Vast.ai JSON filter object merged over the generated filters, e.g. {\"creator_id\":{\"eq\":123}}"`
}

func (d *deps) searchTemplates(ctx context.Context, _ *mcp.CallToolRequest, a SearchTemplatesArgs) (*mcp.CallToolResult, any, error) {
	if a.Limit <= 0 {
		a.Limit = 20
	}
	f := map[string]any{}
	rec := true
	if a.Recommended != nil {
		rec = *a.Recommended
	}
	if rec {
		f["recommended"] = map[string]any{"eq": true}
	}
	if a.RawFilters != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(a.RawFilters), &extra); err != nil {
			return d.errResult(fmt.Errorf("raw_filters is not valid JSON: %w", err))
		}
		for k, v := range extra {
			f[k] = v
		}
	}
	res, err := d.c.SearchTemplates(ctx, f)
	if err != nil {
		return d.errResult(err)
	}
	return d.jsonResult(summarizeTemplates(res, a.Name, a.Limit))
}

var templateFields = []string{"id", "hash_id", "name", "image", "tag", "desc", "recommended", "use_ssh", "jup_direct", "ssh_direct", "docker_login_repo", "recommended_disk_space", "count_created", "creator_id"}

// summarizeTemplates trims template rows to the useful fields and applies a
// case-insensitive substring match on name/image/desc (the API's own "name"
// filter is an exact match).
func summarizeTemplates(res any, name string, limit int) any {
	m, ok := res.(map[string]any)
	if !ok {
		return res
	}
	list, ok := m["templates"].([]any)
	if !ok {
		return []map[string]any{}
	}
	needle := strings.ToLower(name)
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		t, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if needle != "" {
			hay := strings.ToLower(fmt.Sprint(t["name"], " ", t["image"], " ", t["desc"]))
			if !strings.Contains(hay, needle) {
				continue
			}
		}
		out = append(out, pick(t, templateFields))
		if len(out) >= limit {
			break
		}
	}
	return out
}
