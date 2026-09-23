package service

import (
	"context"
	"strings"
)

// plazaListedModelCatalog is the same model set GET /v1/models reads from
// schedulable accounts. Nil keeps the plaza on channel mapping and pricing only.
type plazaListedModelCatalog interface {
	GetAvailableModels(ctx context.Context, groupID *int64, platform string) []string
}

// SetListedModelCatalog attaches the account model list used by /v1/models.
// Models present there are shown even when the channel has no custom price.
func (s *ModelPlazaService) SetListedModelCatalog(catalog plazaListedModelCatalog) {
	if s == nil {
		return
	}
	s.listedModels = catalog
}

// ProvideModelPlazaService 创建模型广场，并接上 /v1/models 同一份账号模型列表。
// 渠道里已经有的模型不在这里覆盖；没渠道价的稍后用官方目录价展示。
func ProvideModelPlazaService(
	channelRepo ChannelRepository,
	groupRepo GroupRepository,
	pricingService *PricingService,
	billingService *BillingService,
	resolver *ModelPricingResolver,
	gateway *GatewayService,
) *ModelPlazaService {
	svc := NewModelPlazaService(channelRepo, groupRepo, pricingService, billingService, resolver)
	svc.SetListedModelCatalog(gateway)
	return svc
}

// appendAccountListedModels adds models the public model list would return for
// this group when the channel never priced or mapped them. Existing channel
// rows win. A missing channel price is filled later from the official catalog.
func (s *ModelPlazaService) appendAccountListedModels(ctx context.Context, pg *PlazaGroup, g *Group) {
	if s == nil || s.listedModels == nil || pg == nil || g == nil || g.ID <= 0 {
		return
	}
	seen := make(map[string]struct{}, len(pg.Models))
	for i := range pg.Models {
		seen[plazaModelKey(pg.Models[i].Platform, pg.Models[i].Name)] = struct{}{}
	}
	gid := g.ID
	for _, platform := range GroupCatalogPlatforms() {
		if !isConcreteRequestPlatform(platform) {
			continue
		}
		if g.Platform != PlatformComposite && g.Platform != platform {
			continue
		}
		for _, name := range s.listedModels.GetAvailableModels(ctx, &gid, platform) {
			name = strings.TrimSpace(name)
			if name == "" || strings.Contains(name, "*") {
				continue
			}
			if g.ModelAllowlistEnabled() && !g.ModelAllowlist.Allows(name) {
				continue
			}
			key := plazaModelKey(platform, name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			pg.Models = append(pg.Models, PlazaModel{
				Name:     name,
				Platform: platform,
			})
		}
	}
}

func plazaModelKey(platform, name string) string {
	return strings.ToLower(platform) + "\x00" + strings.ToLower(name)
}
