package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// PlazaOfficialPricing 模型广场展示用的参考价（USD per token），与计费同源：
// LiteLLM → 内置兜底价卡 → 模型策略。字段为 nil 表示该项缺失（0 视为未配置）。
// 管理员给模型配过价时改显示配置价，见 configuredReferencePricing：
// 分组价卡生效时 Custom=true；渠道定价生效时用渠道价，不打标记。
type PlazaOfficialPricing struct {
	InputPrice        *float64
	OutputPrice       *float64
	CacheWritePrice   *float64 // 5m 缓存写入（= LiteLLM cache_creation）
	CacheWrite1hPrice *float64 // 1h 缓存写入，仅计费会区分 5m/1h 时给出
	CacheReadPrice    *float64
	// Intervals 长上下文阶梯（多档时给出）。目录价不受分组开关影响；
	// 配置价（分组价卡/渠道改价）取计费阶梯表，与实付逐档对应。
	Intervals []PricingInterval
	// Custom 为 true 表示上述起步价来自分组自定义价卡，而不是官方目录价。
	Custom bool
}

// PlazaModel 模型广场中单个模型条目：按实收口径合成的展示定价 + 官方参考价。
type PlazaModel struct {
	Name            string
	Platform        string
	Pricing         *ChannelModelPricing
	OfficialPricing *PlazaOfficialPricing
	// LongContextBasis 多档时的计价基准（整单 / 仅超出部分），单档为空。
	LongContextBasis ContextPricingBasis
	// TimePricing 计费会生效的分时倍率时段；无分时为 nil。
	TimePricing *TimePricingSchedule
	// Metadata 描述性元数据（上下文、最大输出、模态、思考/工具能力），与
	// /v1/models 走同一份 ModelMetadataCatalog；nil 表示目录不可用或解析不到，
	// 此时前端只展示定价，不报错。
	Metadata *ModelMetadata
}

// PlazaGroup 模型广场中以分组为顶层的条目。
//
// 与 AvailableGroupRef 相比多了 Description 与 Models；Models 来自该分组关联渠道的
// 支持模型（普通分组按分组平台隔离，Composite 分组展开关联渠道已配置的
// 具体平台），与「可用渠道」页口径一致。
type PlazaGroup struct {
	ID                 int64
	Name               string
	Description        string
	Platform           string
	SubscriptionType   string
	RateMultiplier     float64
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	IsExclusive        bool
	// 图片按次实付倍率：ImageRateIndependent 为 true 时，图片计费模型的实付
	// = 档位价 × ImageRateMultiplier，不乘分组/用户专属倍率（与计费口径一致）。
	ImageRateIndependent bool
	ImageRateMultiplier  float64
	// LongContextPricingEnabled 分组是否按上下文长度应用阶梯价；关闭时模型展示的是最低档。
	LongContextPricingEnabled bool
	Models                    []PlazaModel
}

// ModelPlazaService 聚合模型广场数据。
//
// 模型枚举来自渠道配置；token 模型的展示单价与阶梯由 BillingService 的阶梯表
// 查询给出（与扣费走同一条解析链与计费函数），图片/按次模型沿用渠道/分组档位价。
type ModelPlazaService struct {
	channelRepo    ChannelRepository
	groupRepo      GroupRepository
	pricingService *PricingService
	billingService *BillingService
	resolver       *ModelPricingResolver
	listedModels   plazaListedModelCatalog
	// metadata 与 /v1/models 共用同一份懒加载目录（models.dev 6h 缓存）。
	metadata *ModelMetadataCatalog
}

// NewModelPlazaService 创建模型广场服务。
func NewModelPlazaService(
	channelRepo ChannelRepository,
	groupRepo GroupRepository,
	pricingService *PricingService,
	billingService *BillingService,
	resolver *ModelPricingResolver,
) *ModelPlazaService {
	return &ModelPlazaService{
		channelRepo:    channelRepo,
		groupRepo:      groupRepo,
		pricingService: pricingService,
		billingService: billingService,
		resolver:       resolver,
	}
}

// ListGroups 返回模型广场数据：每个活跃分组附带其可用模型与定价。
//
// 模型枚举口径与 ListAvailable 一致（Active 渠道、SupportedModels ∪ 全局定价回落、
// 平台隔离），仅把顶层从渠道换成分组：
//   - 渠道按 lower(name) 排序后遍历，保证同名模型去重结果确定；
//   - 同分组同名模型「先见者胜」，仅当已存条目无定价而新条目有定价时升级替换；
//   - token 模型的单价与阶梯按实收口径合成（见 ResolveContextPricingSchedule），
//     图片计费模型的档位价按实收口径合成（见 plazaImageDisplayPricing）；
//   - 每个模型附带官方参考价（查不到为 nil）和描述性元数据（目录不可用为 nil）；
//   - 只返回 Models 非空的分组；分组按 RateMultiplier 升序（同倍率按名称），
//     组内模型按名称排序。
//
// 可见性过滤（专属分组）不在此层做，由 handler 按登录态裁剪。
func (s *ModelPlazaService) ListGroups(ctx context.Context) ([]PlazaGroup, error) {
	channels, err := s.channelRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	sort.SliceStable(channels, func(i, j int) bool {
		return strings.ToLower(channels[i].Name) < strings.ToLower(channels[j].Name)
	})

	byGroup := make(map[int64]*PlazaGroup, len(groups))
	groupEnt := make(map[int64]*Group, len(groups))
	order := make([]int64, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		byGroup[g.ID] = &PlazaGroup{
			ID:                        g.ID,
			Name:                      g.Name,
			Description:               g.Description,
			Platform:                  g.Platform,
			SubscriptionType:          g.SubscriptionType,
			RateMultiplier:            g.RateMultiplier,
			PeakRateEnabled:           g.PeakRateEnabled,
			PeakStart:                 g.PeakStart,
			PeakEnd:                   g.PeakEnd,
			PeakRateMultiplier:        g.PeakRateMultiplier,
			IsExclusive:               g.IsExclusive,
			ImageRateIndependent:      g.ImageRateIndependent,
			ImageRateMultiplier:       g.ImageRateMultiplier,
			LongContextPricingEnabled: g.LongContextPricingEnabled,
		}
		groupEnt[g.ID] = g
		order = append(order, g.ID)
	}

	type modelKey struct {
		platform string
		name     string
	}
	// modelIdx[groupID][platform+modelName] = index into byGroup[groupID].Models
	modelIdx := make(map[int64]map[modelKey]int, len(groups))
	for i := range channels {
		ch := &channels[i]
		if ch.Status != StatusActive {
			continue
		}
		ch.normalizeBillingModelSource()
		supported := ch.SupportedModels()
		fillGlobalPricingFallback(s.pricingService, supported)

		for _, gid := range ch.GroupIDs {
			pg, ok := byGroup[gid]
			if !ok {
				continue
			}
			idx := modelIdx[gid]
			if idx == nil {
				idx = make(map[modelKey]int, len(supported))
				modelIdx[gid] = idx
			}
			for j := range supported {
				m := supported[j]
				if pg.Platform == PlatformComposite {
					if !isConcreteRequestPlatform(m.Platform) {
						continue
					}
				} else if m.Platform != pg.Platform {
					continue
				}
				key := modelKey{platform: m.Platform, name: m.Name}
				if at, seen := idx[key]; seen {
					// 先见者胜；仅当已存条目无定价而新条目有定价时升级。
					if pg.Models[at].Pricing == nil && m.Pricing != nil {
						pg.Models[at].Pricing = m.Pricing
					}
					continue
				}
				idx[key] = len(pg.Models)
				pg.Models = append(pg.Models, PlazaModel{
					Name:     m.Name,
					Platform: m.Platform,
					Pricing:  m.Pricing,
				})
			}
		}
	}

	officialMemo := make(map[string]*PlazaOfficialPricing)
	metadataMemo := make(map[string]*ModelMetadata)
	out := make([]PlazaGroup, 0, len(order))
	for _, gid := range order {
		pg := byGroup[gid]
		s.appendAccountListedModels(ctx, pg, groupEnt[gid])
		if len(pg.Models) == 0 {
			continue
		}
		sort.SliceStable(pg.Models, func(i, j int) bool {
			if pg.Models[i].Name != pg.Models[j].Name {
				return pg.Models[i].Name < pg.Models[j].Name
			}
			return pg.Models[i].Platform < pg.Models[j].Platform
		})
		g := groupEnt[gid]
		for j := range pg.Models {
			if pricingNeedsFallback(pg.Models[j].Pricing) {
				one := []SupportedModel{{
					Name:     pg.Models[j].Name,
					Platform: pg.Models[j].Platform,
					Pricing:  pg.Models[j].Pricing,
				}}
				fillGlobalPricingFallback(s.pricingService, one)
				pg.Models[j].Pricing = one[0].Pricing
			}
			sched := s.fillDisplayPricing(ctx, &pg.Models[j], g)
			official := s.lookupOfficialPricing(ctx, pg.Models[j].Name, officialMemo)
			pg.Models[j].OfficialPricing = configuredReferencePricing(official, sched)
			pg.Models[j].Metadata = s.resolvePlazaMetadata(ctx, pg.Models[j].Platform, pg.Models[j].Name, metadataMemo)
		}
		out = append(out, *pg)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RateMultiplier != out[j].RateMultiplier {
			return out[i].RateMultiplier < out[j].RateMultiplier
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// resolvePlazaMetadata resolves descriptive metadata through the shared
// catalogue, memoized per platform+model because the same model shows up in
// several groups. Resolution never errors: an unavailable catalogue (or a model
// the registry and built-in tables cannot describe) yields nil and the plaza
// keeps showing pricing only instead of failing the request.
func (s *ModelPlazaService) resolvePlazaMetadata(ctx context.Context, platform, modelName string, memo map[string]*ModelMetadata) *ModelMetadata {
	if s == nil || s.metadata == nil {
		return nil
	}
	key := plazaModelKey(platform, modelName)
	if cached, ok := memo[key]; ok {
		return cached
	}
	metadata := s.metadata.Resolve(ctx, modelName, platform, nil)
	var resolved *ModelMetadata
	// A bare ID with no descriptive field carries nothing the plaza can render.
	if metadata.DisplayName != "" || metadata.ContextWindow > 0 || metadata.MaxOutputTokens > 0 ||
		len(metadata.InputModalities) > 0 || metadata.Capabilities != nil {
		resolved = &metadata
	}
	memo[key] = resolved
	return resolved
}

// fillDisplayPricing 把模型的展示定价换成实收口径：
// token 模型取计费阶梯表（单价与档位均由真实计费函数得出），
// 图片/按次模型（或阶梯表不可用时）沿用渠道定价与分组图片档位价。
func (s *ModelPlazaService) fillDisplayPricing(ctx context.Context, m *PlazaModel, g *Group) *ContextPricingSchedule {
	if s.billingService != nil && s.resolver != nil {
		sched, err := s.billingService.ResolveContextPricingSchedule(ctx, s.resolver, ContextPricingScheduleInput{
			Model:    m.Name,
			Group:    g,
			Platform: m.Platform,
		})
		if err == nil && sched != nil && len(sched.Tiers) > 0 {
			m.Pricing = withDefaultMaxReasoningEffortMultiplier(plazaPricingFromSchedule(m.Pricing, sched), m.Name)
			if len(sched.Tiers) > 1 {
				m.LongContextBasis = sched.Basis
			}
			m.TimePricing = sched.TimePricing
			return sched
		}
	}
	m.Pricing = withDefaultMaxReasoningEffortMultiplier(plazaImageDisplayPricing(m.Pricing, g), m.Name)
	return nil
}

// configuredReferencePricing 决定「官方价格」列显示什么。
//
// 管理员给这个模型配过价（分组价卡或渠道定价）时，列里要显示他配的价，而不是目录价，
// 否则 实付 = 官方价 × 倍率 对不上（例如渠道配 $1、目录是 $0.22，倍率 0.3x 的实付 $0.30
// 旁边却写着 $0.22）。显示的价取计费阶梯表，与实付同源，档位也一起取阶梯表。
//
//   - 阶梯表为空（图片/按次等非 token 计费）、或定价来自目录：保持目录价。
//   - 来自分组价卡：一律用阶梯表，并标记 Custom（界面上有「自定义价」标记）。
//   - 来自渠道定价：只有首档单价和目录不同才用阶梯表，不打标记——渠道价就是这个渠道的
//     标价。渠道条目只是放行模型、没改价时，仍沿用目录价和目录阶梯，不受分组阶梯开关影响。
func configuredReferencePricing(official *PlazaOfficialPricing, sched *ContextPricingSchedule) *PlazaOfficialPricing {
	if sched == nil || len(sched.Tiers) == 0 {
		return official
	}
	switch sched.Source {
	case PricingSourceGroup:
		return referencePricingFromSchedule(sched, true)
	case PricingSourceChannel:
		if firstTierMatchesOfficial(sched.Tiers[0], official) {
			return official
		}
		return referencePricingFromSchedule(sched, false)
	default:
		return official
	}
}

// referencePricingFromSchedule 把阶梯表压成参考价：首档作起步价，多档时逐档给出绝对单价。
func referencePricingFromSchedule(sched *ContextPricingSchedule, custom bool) *PlazaOfficialPricing {
	first := sched.Tiers[0]
	ref := &PlazaOfficialPricing{
		InputPrice:        first.Input,
		OutputPrice:       first.Output,
		CacheWritePrice:   first.CacheWrite,
		CacheWrite1hPrice: first.CacheWrite1h,
		CacheReadPrice:    first.CacheRead,
		Custom:            custom,
	}
	if len(sched.Tiers) > 1 {
		ref.Intervals = plazaIntervalsFromTiers(sched.Tiers)
	}
	return ref
}

// firstTierMatchesOfficial 判断计费首档是否和目录价一致（输入/输出/缓存写入/缓存读取）。
// 不比 1h 缓存写入：阶梯表对它总会探出一个值，目录只在支持 5m/1h 分档时才给。
func firstTierMatchesOfficial(tier ContextPricingTier, official *PlazaOfficialPricing) bool {
	if official == nil {
		return false
	}
	return samePrice(tier.Input, official.InputPrice) &&
		samePrice(tier.Output, official.OutputPrice) &&
		samePrice(tier.CacheWrite, official.CacheWritePrice) &&
		samePrice(tier.CacheRead, official.CacheReadPrice)
}

// samePrice 比较两个单价，nil 与 0 都表示没有价格。探针值保留 12 位有效数字，容差取相对 1e-9。
func samePrice(a, b *float64) bool {
	var x, y float64
	if a != nil {
		x = *a
	}
	if b != nil {
		y = *b
	}
	if x == y {
		return true
	}
	return math.Abs(x-y) <= 1e-9*math.Max(math.Abs(x), math.Abs(y))
}

func withDefaultMaxReasoningEffortMultiplier(pricing *ChannelModelPricing, model string) *ChannelModelPricing {
	if pricing == nil || pricing.MaxReasoningEffortMultiplier != nil {
		return pricing
	}
	multiplier := defaultMaxReasoningEffortMultiplier(model)
	if multiplier == nil {
		return pricing
	}
	cloned := pricing.Clone()
	cloned.MaxReasoningEffortMultiplier = multiplier
	return &cloned
}

// plazaPricingFromSchedule 把阶梯表压成展示用的 ChannelModelPricing：
// 平价取首档单价，多档时 Intervals 逐档给出绝对单价；图片/按次字段沿用原始定价。
func plazaPricingFromSchedule(raw *ChannelModelPricing, sched *ContextPricingSchedule) *ChannelModelPricing {
	out := &ChannelModelPricing{BillingMode: BillingModeToken}
	if raw != nil {
		out.ImageInputPrice = raw.ImageInputPrice
		out.ImageOutputPrice = raw.ImageOutputPrice
		out.PerRequestPrice = raw.PerRequestPrice
		out.MaxReasoningEffortMultiplier = raw.MaxReasoningEffortMultiplier
	}
	first := sched.Tiers[0]
	out.InputPrice = first.Input
	out.OutputPrice = first.Output
	out.CacheWritePrice = first.CacheWrite
	out.CacheWrite1hPrice = first.CacheWrite1h
	out.CacheReadPrice = first.CacheRead
	if len(sched.Tiers) > 1 {
		out.Intervals = plazaIntervalsFromTiers(sched.Tiers)
	}
	return out
}

func plazaIntervalsFromTiers(tiers []ContextPricingTier) []PricingInterval {
	intervals := make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		intervals = append(intervals, PricingInterval{
			MinTokens:         t.MinTokens,
			MaxTokens:         t.MaxTokens,
			TierLabel:         t.Label,
			InputPrice:        t.Input,
			OutputPrice:       t.Output,
			CacheWritePrice:   t.CacheWrite,
			CacheWrite1hPrice: t.CacheWrite1h,
			CacheReadPrice:    t.CacheRead,
			SortOrder:         i,
		})
	}
	return intervals
}

// plazaImageDisplayPricing 为图片计费模型合成展示定价，使档位价与实收口径一致：
// 每档（1K/2K/4K）单价 = 分组图片价 > 渠道同档位价 > 渠道默认按次价，无价的档不展示。
// 分组未配任何图片价、或定价非图片模式时原样返回。返回克隆，不修改入参
// （渠道定价指针指向缓存共享数据）。
func plazaImageDisplayPricing(p *ChannelModelPricing, g *Group) *ChannelModelPricing {
	if p == nil || g == nil || p.BillingMode != BillingModeImage {
		return p
	}
	if g.ImagePrice1K == nil && g.ImagePrice2K == nil && g.ImagePrice4K == nil {
		return p
	}
	channelTierPrice := func(label string) *float64 {
		for i := range p.Intervals {
			if p.Intervals[i].TierLabel == label && p.Intervals[i].PerRequestPrice != nil {
				return p.Intervals[i].PerRequestPrice
			}
		}
		return p.PerRequestPrice
	}
	tiers := []struct {
		label      string
		groupPrice *float64
	}{
		{"1K", g.ImagePrice1K},
		{"2K", g.ImagePrice2K},
		{"4K", g.ImagePrice4K},
	}
	clone := *p
	clone.Intervals = make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		price := t.groupPrice
		if price == nil {
			price = channelTierPrice(t.label)
		}
		if price == nil {
			continue
		}
		v := *price
		clone.Intervals = append(clone.Intervals, PricingInterval{
			TierLabel:       t.label,
			PerRequestPrice: &v,
			SortOrder:       i,
		})
	}
	return &clone
}

// lookupOfficialPricing 查询模型的官方参考价（与计费同源：LiteLLM → 内置兜底 → 模型策略），
// 带 memo 避免同名模型重复解析。官方阶梯按无分组、无渠道的口径查阶梯表。
// billingService 为 nil（测试场景）或查不到时返回 nil。
func (s *ModelPlazaService) lookupOfficialPricing(ctx context.Context, modelName string, memo map[string]*PlazaOfficialPricing) *PlazaOfficialPricing {
	if s.billingService == nil {
		return nil
	}
	if cached, ok := memo[modelName]; ok {
		return cached
	}
	var result *PlazaOfficialPricing
	if mp, err := s.billingService.GetModelPricing(modelName); err == nil && mp != nil {
		result = &PlazaOfficialPricing{
			InputPrice:      nonZeroPtr(mp.InputPricePerToken),
			OutputPrice:     nonZeroPtr(mp.OutputPricePerToken),
			CacheWritePrice: nonZeroPtr(mp.CacheCreationPricePerToken),
			CacheReadPrice:  nonZeroPtr(mp.CacheReadPricePerToken),
		}
		// 计费只在支持 5m/1h 分档时使用 1h 价，其余情况 1h 价对用户无意义。
		if mp.SupportsCacheBreakdown {
			result.CacheWrite1hPrice = nonZeroPtr(mp.CacheCreation1hPrice)
		}
		if s.resolver != nil {
			sched, schedErr := s.billingService.ResolveContextPricingSchedule(ctx, s.resolver, ContextPricingScheduleInput{Model: modelName})
			if schedErr == nil && sched != nil && len(sched.Tiers) > 1 {
				result.Intervals = plazaIntervalsFromTiers(sched.Tiers)
			}
		}
		if result.InputPrice == nil && result.OutputPrice == nil && result.CacheWritePrice == nil &&
			result.CacheWrite1hPrice == nil && result.CacheReadPrice == nil && len(result.Intervals) == 0 {
			result = nil
		}
	}
	memo[modelName] = result
	return result
}
