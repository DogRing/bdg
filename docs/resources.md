# Resources — 원자원 카탈로그 & 소싱 (Earth/Mineral) — Subsystem Plan

Concept & rationale: `docs/design.md §5`(바탕재료 sand/soil/river/mountain/sea + 속성), `§9`(경제 — `currency` 이연),
`docs/materials.md`(FINAL recipe model · `ore_node`/`Mine`/`source:` · Q4 "추출=terrain 변형" · Q5 "광물=유한·희소").
이 문서 = **raw 자원 카탈로그 + 통합 소싱 모델** — `materials`(변환)·`world-gen`(분포)·`flora`/`fauna`(다른 source 종류)가 참조.
**결정 표면**이고 module SPEC 아님.

관련 모듈: `content/objects.yaml`(자원 item/object_kind + 재료 tag + `source:` 블록), `content/recipes.yaml`(제련·소성·도구 사슬),
`engine/mind/actions`(`Mine` — terrain-driven 추출 **경로 추가**), `engine/world`(추출 apply·SetTerrain), `engine/kernel/expr`(§6 yield).

---

## 0. Decisions locked (사람 확정 2026-06-27 — 재논쟁 금지)

1. **Phase-1 earth/mineral 카탈로그 6:** `stone` · `clay` · `coal` · `copper` · `iron` · `gold`. (식물=flora, 동물=fauna,
   물=`water_source` — 본 문서 밖, 소싱 맵에서 *참조만*. 본 문서 = earth/mineral 집합 + 통합 소싱 모델.)
2. **소싱 의도:** `stone`=어디서나 · `clay`=흙(soil) 주변 · `coal`/`copper`/`iron`/`gold`=산/땅속.
3. **하이브리드 소싱(materials Xm1 확장):** 풍부(`stone`/`clay`)=**terrain-driven 추출**(노드 없이 §6 yield over 지형
   base material; materials **Q4 부활**); 희소 금속(`coal`/`copper`/`iron`/`gold`)=**유한 `ore_node`**(Xm1, 고갈→SetTerrain;
   희소→경제 분쟁, Q5). ⇒ `materials` Mine SPEC에 **terrain-driven 추출 경로 추가** 필요(노드 path와 병존).
4. **2D only** — 깊이/지하층 없음. "땅속"=지표에서 `Mine`(D11 연속 2D).
5. **`gold` = 희소 귀중자원, `currency` 미연결**(economy/화폐는 후속 서브시스템; 카탈로그엔 유지, 화폐 wiring 없음).
6. **품질 등급 없음** — 추출 수량/품질 = §6(Dexterity) roll(광석 grade 필드 없음).
7. **제련/가공 = 레시피만** — `station:furnace` ambient + `coal` consume → 금속; clay→소성→토기. **새 메커니즘 0**
   (materials FINAL; planner가 채굴→제련→단조 다단계 plan 조립, D3).
8. **tier = 창발:** stone/clay(무제련) → copper(제련) → iron(제련+석탄 연료) → gold(부). **하드코딩 테크트리 아님**
   (D2/D3 — 레시피 input 가용성에서 창발).

### Catalog (→ content/objects.yaml; 정확한 tag는 R4)
| 자원 | 소싱 메커니즘 | terrain affinity | 1차 용도 |
|---|---|---|---|
| `stone` | terrain-driven | 어디서나 | 석기·건축 (무제련) |
| `clay` | terrain-driven | soil (R2) | 토기 (소성) |
| `coal` | 유한 `ore_node` | mountain | 연료(제련 consume)·불 |
| `copper` | 유한 `ore_node` | mountain | 제련→구리 도구 |
| `iron` | 유한 `ore_node` | mountain | 제련(+석탄)→철 도구 |
| `gold` | 유한 `ore_node`(희소) | mountain | 부/귀중 (currency 미연결) |

---

## 1. Resolutions — 사람 확정 (2026-06-27): **R1~R7 전부 추천대로 (R2만 조정)**
> 아래 표가 권위. 각 R의 옵션 상세·근거는 그대로 기록(재논쟁 금지).

- **R1** terrain-driven **§6 yield over 지형 base material** — `Mine` 타겟 = `ore_node` 있으면 노드 path, 없으면 terrain-cell path(같은 `tool:digging`); `stone` 고갈≈0(풍부)·`clay` soil 적합도. ⇒ `materials` Mine SPEC에 **terrain-driven 경로 추가**.
- **R2 (조정)** `clay` = **아무 `soil` terrain** — **물 근접 *불요*** (비가 moisture를 공급하므로). `moisture`(강우)는 yield를 *변조*할 수 있으나 **위치 gate 아님**. (rec (b) 물근접 → **(a) soil 전역**으로 변경.)
- **R3** 광물별 object_kind: `iron_ore`·`copper_ore`·`coal_seam`·`gold_vein` (generic `ore_node` 특수화).
- **R4** tags: `stone`→`stone_stock` · `clay`→`clay_stock` · `coal`→`fuel` · copper/iron ore→`ore:copper`/`ore:iron` → 산출 `metal:copper`/`metal:iron` · `gold`→`precious`/`metal:gold`.
- **R5** `gold` = `precious` tag + Value 'wealth' hook(없으면 economy까지 inert, seam 예약).
- **R6** `recipes.yaml`: `smelt_copper` · `smelt_iron`(+`coal` fuel) · `fire_clay`; `furnace`/`kiln` = `Build`로 짓는 `station:*` 구조물(불&빛 서브시스템 불필요).
- **R7** affinity 계약만(ore→산·clay→soil·stone→any); 분포 generator = **world-gen Tier-2** 이연.
> glossary 신규: `fuel` · `ore:*` · `metal:*` · `precious` · `stone_stock`/`clay_stock` · `station:furnace`(·`station:kiln`) · 광물 kind명.

### (옵션 상세 — 근거 기록)

- **R1 — terrain-driven 추출 메커니즘 (stone/clay).** 노드 없이 `Mine`이 terrain 셀에서 yield. options:
  (a) **§6 yield over 지형 base material/속성**(mountain→소량 돌, soil→점토 등), 추출 누적 시 terrain 속성↓→임계서
  `SetTerrain`(Q4 — 유한화); (b) base material별 고정 yield, `stone`=고갈 없음(사실상 무한)·`clay`=soil 한정; (c) terrain
  hidden source counter. **rec: (a)** — §6 yield + `stone`은 고갈률 ≈0(풍부), `clay`는 soil 적합도 가중; `Mine` 타겟 =
  `ore_node` 있으면 노드 path, 없으면 terrain-cell path(같은 `tool:digging` gate). `[materials Mine SPEC 확장 — §0-3]`
- **R2 — `clay` 위치 정밀.** options: (a) 아무 `soil` terrain; (b) **`soil` + 물 근접**(로드맵 "점토=물가" ∧ 사용자 "흙 주변");
  (c) `moisture` §6 임계. **rec: (b)** soil base ∧ 물근접(§6 over base + `moisture`) — "흙 주변"+"물가" 둘 다 만족, 지리적 의미.
- **R3 — `ore_node` 콘텐츠 형태.** options: (a) 단일 `ore_node` + `mineral` 파라미터/yield tag; (b) **광물별 object_kind**
  (`iron_ore`/`copper_ore`/`coal_seam`/`gold_vein`). **rec: (b)** — per-kind yield/희소도/tag 명료(flora 종별 선례); 현 generic
  `ore_node`는 베이스 폐기/특수화. `[content shape, D10]`
- **R4 — 자원별 material tag 어휘.** 각 자원이 다는 `tags`(레시피/도구 query 대상). **rec:** `stone`→`stone_stock`(knap 시
  `blade_stock` 자격 가능); `clay`→`clay_stock`; `coal`→**`fuel`**(제련 consume); `copper`/`iron` ore→`ore:copper`/`ore:iron`
  (smelt input) → 산출 `metal:copper`/`metal:iron`(+도구 query `blade_stock`/`shaft_stock` 등); `gold`→`precious`/`metal:gold`.
  `[glossary 신규: fuel · ore:* · metal:* · precious]`
- **R5 — `gold` want-hook (currency 없이 어떻게 욕구화?).** options: (a) **`precious` desirability tag** + Value 'wealth/beauty'
  dimension hook(있으면); (b) economy까지 inert; (c) 장식/status item. **rec: (a)** — `precious` tag + Value hook(없으면 (b)로
  동작, seam만 예약); 화폐화는 economy/currency phase. `[economy seam]`
- **R6 — 제련 사슬 author + `furnace` 구조물.** 대표 레시피 + 용광로=지은 구조물. **rec:** `recipes.yaml`에 `smelt_copper`
  (copper ore consume + `station:furnace` ambient → copper), `smelt_iron`(iron ore + `coal`(fuel) consume + furnace → iron),
  `fire_clay`(clay + `station:kiln`|furnace → pottery); `furnace`/`kiln` = `Build`로 짓는 object_kind(`station:*` tag).
  **불&빛 서브시스템 불필요**(레시피 ambient+consume로 충분). `[glossary: station:furnace (· station:kiln)]`
- **R7 — 분포 → world-gen 계약.** 배치(산에 광맥·soil/물가에 점토·돌 도처)는 **world-gen Tier-2 소관**. 여기선 *계약*만:
  자원↔terrain affinity(ore_node→mountain, clay→soil∧물근접, stone→any). **rec:** affinity 표(§0 Catalog)만 노출, generator 이연.

---

## 2. Phases / integration
- **콘텐츠:** `objects.yaml` 6 자원 kind(R3) + material tags(R4) + `ore_node` `source:` 블록; `recipes.yaml` 제련/소성/도구
  사슬(R6); `content/schema/*` 확장.
- **`materials` Mine SPEC 확장:** terrain-driven 추출 경로(R1) — 기존 노드 path와 병존(같은 `Mine`/`tool:digging`).
- **world-gen Tier-2(후속):** R7 affinity로 분포 생성(강 hydrology 포함 — 별도 게이트).
- **새 엔진 메커니즘 = `Mine` terrain-driven path 하나뿐.** 나머지(자원 종류·tier·제련)는 전부 content/recipe/§6 데이터(D10).

## 3. Notes / flags
- **불변식:** D2(테크트리 창발, 하드코딩 금지) · D4(tag/§6 파생) · D10(자원=content+schema) · D11(2D, 깊이층 없음) · D12.
- **신규 용어(glossary 등재 대상):** `fuel` · `ore:*` · `metal:*` · `precious` · `stone_stock`/`clay_stock` · `station:furnace`(·`station:kiln`) ·
  광물 object_kind 명(`iron_ore`/`copper_ore`/`coal_seam`/`gold_vein`, R3 확정 시).
- **materials Q4↔Xm1 화해:** Xm1(ore_node)=희소 금속, Q4(terrain 변형)=풍부 stone/clay → **둘 다 사용**(하이브리드 §0-3).
- **gold:** phase-1 기능 용도 없음(연료·도구 아님) — `precious` value hook(R5) 외엔 economy 전까지 대체로 inert(의도된 seam).
- **°C 무관:** 자원 추출/제련은 climate temperature operand 미사용(decay/flora와 달리 °C ripple 없음).
