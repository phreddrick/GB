# Production & Economy Constants Audit

**Date:** 2026-06-28  
**Source:** C `tweakables.h` vs. Go `internal/config/config.go` and related files  
**Status:** ✅ **Most constants verified; identified location discrepancies**

---

## Summary

The Go port has **~95% coverage** of tweakables constants. Most are correctly migrated to `config.go`, with a few located in specialized files (`build.go` for ship costs) and some that don't need migration (display characters, type constants).

### Key Findings

✅ **In config.go (Default() values verified)**
- 85+ numeric constants correctly ported with matching C source values
- All timing, physics, and economy constants present

📍 **Scattered in other files (by design)**
- Ship build costs (gunCost, crewCost, etc.) → `build.go:45-53` (private const)
- Terrain type constants → model package
- Display characters → rendering layer

❌ **Missing from Go (but needed)**
- `MAX_X`, `MAX_Y` — planet map dimensions
- `RATIOXY` — map aspect ratio
- `LIMITED_RESOURCES` flag
- `UP_BID` — commodity market bid increment
- `AFVFUEL_COST` — AFV fuel cost (reference only; no AFV implementation yet)

⚠️ **Needs Verification**
- Victory point system constants (`VICT_*` macros that use `int_rand()`)
- Production formulas match C exactly
- Some planetary constants (gravity, terrain effects)

---

## Detailed Comparison

### ✅ VERIFIED: Matching Constants in config.go

| C Name | Go Field | C Value | Go Default | Status |
|--------|----------|---------|-----------|--------|
| `MOVE_FACTOR` | `MoveFactor` | 1 | 1.0 | ✅ |
| `DEFAULT_UPDATE_TIME` | `DefaultUpdateMinutes` | 30*60 s | 30 min | ✅ |
| `MOVES_PER_UPDATE` | `MovesPerUpdate` | 3 | 3 | ✅ |
| `LIMIT_APs` | `LimitAPs` | 255 | 255 | ✅ |
| `MAXCOMMSTRSIZE` | `MaxCommStrSize` | 250 | 250 | ✅ |
| `COMMANDSIZE` | `CommandSize` | 42 | 42 | ✅ |
| `MAXARGS` | `MaxArgs` | 256 | 256 | ✅ |
| `LOGIN_NAME_SIZE` | `LoginNameSize` | 64 | 64 | ✅ |
| `NUM_TIMES_TO_WAIT_FOR_LOCK` | `NumTimesToWaitLock` | 200 | 200 | ✅ |
| `UNIVSIZE` | `UnivSize` | 150000 | 150000 | ✅ |
| `SYSTEMSIZE` | `SystemSize` | 2000 | 2000 | ✅ |
| `PLORBITSIZE` | `PLOrbitSize` | 50 | 50 | ✅ |
| `MAX_SECT_POPN` | `MaxSectPopn` | 32767 | 32767 | ✅ |
| `TOXMAX` | `ToxMax` | 20 | 20 | ✅ |
| **Ship Mass Factors** ||||
| `MASS_FUEL` | `MassFuel` | 0.05 | 0.05 | ✅ |
| `MASS_RESOURCE` | `MassResource` | 0.1 | 0.1 | ✅ |
| `MASS_DESTRUCT` | `MassDestruct` | 0.15 | 0.15 | ✅ |
| `MASS_ARMOR` | `MassArmor` | 1.0 | 1.0 | ✅ |
| `MASS_SIZE` | `MassSize` | 0.2 | 0.2 | ✅ |
| `MASS_HANGER` | `MassHanger` | 0.1 | 0.1 | ✅ |
| `MASS_GUNS` | `MassGuns` | 0.2 | 0.2 | ✅ |
| **Ship Size Factors** ||||
| `SIZE_GUNS` | `SizeGuns` | 0.1 | 0.1 | ✅ |
| `SIZE_CREW` | `SizeCrew` | 0.01 | 0.01 | ✅ |
| `SIZE_RESOURCE` | `SizeResource` | 0.02 | 0.02 | ✅ |
| `SIZE_FUEL` | `SizeFuel` | 0.01 | 0.01 | ✅ |
| `SIZE_DESTRUCT` | `SizeDestruct` | 0.02 | 0.02 | ✅ |
| **Production** ||||
| `RESOURCE_PRODUCTION` | `ResourceProduction` | 0.00008 | 0.00008 | ✅ |
| `FUEL_PRODUCTION` | `FuelProduction` | 0.00008 | 0.00008 | ✅ |
| `DEST_PRODUCTION` | `DestProduction` | 0.00008 | 0.00008 | ✅ |
| `POPN_PROD` | `PopnProd` | 0.3 | 0.3 | ✅ |
| `EFF_PROD` | `EffProd` | 0.20 | 0.20 | ✅ |
| `FACTOR_FERT_SUPPORT` | `FactorFertSupport` | 1 | 1.0 | ✅ |
| `HABITAT_PROD_RATE` | `HabitatProdRate` | 0.05 | 0.05 | ✅ |
| `HABITAT_POP_RATE` | `HabitatPopRate` | 0.20 | 0.20 | ✅ |
| **Combat** ||||
| `SECTOR_DAMAGE` | `SectorDamage` | 0.3 | 0.3 | ✅ |
| `SHIP_DAMAGE` | `ShipDamage` | 2.0 | 2.0 | ✅ |
| `FACTOR_DAMAGE` | `FactorDamage` | 2.0 | 2.0 | ✅ |
| `FACTOR_DESTPLANET` | `FactorDestPlanet` | 0.35 | 0.35 | ✅ |
| `PLAN_FIRE_LIM` | `PlanFireLim` | 20 | 20 | ✅ |
| `MECH_ATTACK` | `MechAttack` | 3.0 | 3.0 | ✅ |
| **Navigation** ||||
| `FUEL_USE` | `FuelUse` | 0.02 | 0.02 | ✅ |
| `LAUNCH_GRAV_MASS_FACTOR` | `LaunchGravMassFactor` | 0.18 | 0.18 | ✅ |
| `LAND_GRAV_MASS_FACTOR` | `LandGravMassFactor` | 0.0145 | 0.0145 | ✅ |
| `FUEL_GAS_ADD` | `FuelGasAdd` | 5.0 | 5.0 | ✅ |
| `FUEL_GAS_ADD_TANKER` | `FuelGasAddTanker` | 100.0 | 100.0 | ✅ |
| `FUEL_GAS_ADD_HABITAT` | `FuelGasAddHabitat` | 200.0 | 200.0 | ✅ |
| `FUEL_GAS_ADD_STATION` | `FuelGasAddStation` | 100.0 | 100.0 | ✅ |
| `HYPER_DRIVE_FUEL_USE` | `HyperDriveFuelUse` | 5.0 | 5.0 | ✅ |
| `HYPER_DIST_FACTOR` | `HyperDistFactor` | 200.0 | 200.0 | ✅ |
| `HYPER_DRIVE_READY_CHARGE` | `HyperDriveReadyCharge` | 1 | 1 | ✅ |
| `DIST_TO_LAND` | `DistToLand` | 10.0 | 10.0 | ✅ |
| `DIST_TO_DOCK` | `DistToDock` | 10.0 | 10.0 | ✅ |
| `STRIKE_DISTANCE_FACTOR` | `StrikeDistanceFactor` | 5.5 | 5.5 | ✅ |
| `COMPLEXITY_FACTOR` | `ComplexityFactor` | 10.0 | 10.0 | ✅ |
| `GRAV_FACTOR` | `GravFactor` | 0.0025 | 0.0025 | ✅ |
| `PLANETGRAVCONST` | `PlanetGravConst` | 0.05 | 0.05 | ✅ |
| `SYSTEMGRAVCONST` | `SystemGravConst` | 150000.0 | 150000.0 | ✅ |
| `FUEL_MANEUVER` | `FuelManeuver` | 0.3 | 0.3 | ✅ |
| **Tech** ||||
| `TECH_INVEST` | `TechInvest` | 0.01 | 0.01 | ✅ |
| `TECH_SCALE` | `TechScale` | 2.0 | 2.0 | ✅ |
| `TECH_SEE_STABILITY` | `TechSeeStability` | 15 | 15 | ✅ |
| `TECH_EXPLORE` | `TechExplore` | 10 | 10 | ✅ |
| **Troops** ||||
| `ENLIST_TROOP_COST` | `EnlistTroopCost` | 5 | 5 | ✅ |
| `UPDATE_TROOP_COST` | `UpdateTroopCost` | 1 | 1 | ✅ |
| `MOB_COST` | `MobCost` | 0.00 | 0.00 | ✅ |
| `FACTOR_MOBPROD` | `FactorMobProd` | 0.06 | 0.06 | ✅ |
| `RESOURCE_DEPLETION` | `ResourceDepletion` | 0.0 | 0.0 | ✅ |
| `MESO_POP_SCALE` | `MesoPopScale` | 20000.0 | 20000.0 | ✅ |
| **Terraforming** ||||
| `FUEL_COST_TERRA` | `FuelCostTerra` | 3.0 | 3.0 | ✅ |
| `FUEL_COST_QUARRY` | `FuelCostQuarry` | 2.0 | 2.0 | ✅ |
| `FUEL_COST_PLOW` | `FuelCostPlow` | 2.0 | 2.0 | ✅ |
| `RES_COST_DOME` | `ResCostDome` | 1 | 1.0 | ✅ |
| `RES_COST_WPLANT` | `ResCostWPlant` | 1 | 1.0 | ✅ |
| `FUEL_COST_WPLANT` | `FuelCostWPlant` | 1.0 | 1.0 | ✅ |
| `ENVIR_DAMAGE_TOX` | `EnvirDamageTox` | 70 | 70 | ✅ |
| **Repair** ||||
| `REPAIR_RATE` | `RepairRate` | 25.0 | 25.0 | ✅ |
| `SECTOR_REPAIR_COST` | `SectorRepairCost` | 10 | 10 | ✅ |
| `NATURAL_REPAIR` | `NaturalRepair` | 5 | 5 | ✅ |
| **Misc** ||||
| `VN_RES_TAKE` | `VNResTake` | 0.5 | 0.5 | ✅ |
| `AP_FACTOR` | `APFactor` | 50.0 | 50.0 | ✅ |
| `DISSIPATE` | `Dissipate` | 80 | 80 | ✅ |
| `POD_THRESHOLD` | `PodThreshold` | 18 | 18 | ✅ |
| `POD_DECAY` | `PodDecay` | 4 | 4 | ✅ |
| `SPORE_SUCCESS_RATE` | `SporeSuccessRate` | 25 | 25 | ✅ |
| `MAXGOVERNORS` | `MaxGovernors` | 5 | 5 | ✅ |
| **Victory** ||||
| `VICTORY_PERCENT` | `VictoryPercent` | 10 | 10 | ✅ |
| `VICTORY_UPDATES` | `VictoryUpdates` | 5 | 5 | ✅ |
| **Telegram** ||||
| `AUTO_TELEG_SIZE` | `AutoTelegSize` | 2000 | 2000 | ✅ |
| `TELEG_TRANS_RPCOST` | `TelegTransRPCost` | 5 | 5 | ✅ |
| `TELEG_TRANS_AMT` | `TelegTransAmt` | 0.45 | 0.45 | ✅ |
| `TELEG_LETTERS` | `TelegLetters` | 7 | 7 | ✅ |
| `TELEG_MAX_AUTO` | `TelegMaxAuto` | 7 | 7 | ✅ |
| `CREAT_UNIV_ITERAT` | `CreatUnivIterat` | 10 | 10 | ✅ |
| **Movement** ||||
| `POPN_MOVE_SCALE_1` | `PopnMoveScale1` | 400 | 400 | ✅ |
| `POPN_MOVE_SCALE_2` | `PopnMoveScale2` | 3000 | 3000 | ✅ |
| `SHIP_MOVE_SCALE` | `ShipMoveScale` | 3.0 | 3.0 | ✅ |
| **Markets** ||||
| `MERCHANT_LENGTH` | `MerchantLength` | 200000.0 | 200000.0 | ✅ |
| `INCOME_FACTOR` | `IncomeFactor` | 0.002 | 0.002 | ✅ |
| `INSURG_FACTOR` | `InsurgFactor` | 1 | 1.0 | ✅ |

### ✅ VERIFIED: Ship Build Costs (in build.go:45-53)

| C Name | Go Name | Value | Location |
|--------|---------|-------|----------|
| `GUN_COST` | `gunCost` | 1.00 | build.go:45 |
| `CREW_COST` | `crewCost` | 0.05 | build.go:46 |
| `CARGO_COST` | `cargoCost` | 0.05 | build.go:47 |
| `FUEL_COST` | `fuelCostBld` | 0.05 | build.go:48 |
| `AMMO_COST` | `ammoCost` | 0.05 | build.go:49 |
| `SPEED_COST` | `speedCost` | 0.50 | build.go:50 |
| `CEW_COST` | `cewCost` | 0.003 | build.go:51 |
| `ARMOR_COST` | `armorCost` | 3.50 | build.go:52 |
| `HANGER_COST` | `hangerCost` | 0.50 | build.go:53 |

**Status:** ✅ **Correct location** — Kept private to `build.go` since they're only used there.

### ⚠️ NEEDS VERIFICATION: Special Cases

#### 1. **Victory Points** — Random Range Constants
C source uses `int_rand()` macro for victory points:
```c
#define EARTH_POINTS    int_rand(5,8)
#define ASTEROID_POINTS 1
#define MARS_POINTS     int_rand(2,3)
#define ICEBALL_POINTS  int_rand(2,3)
#define GASGIANT_POINTS int_rand(8,20)
#define WATER_POINTS    int_rand(2,3)
#define FOREST_POINTS   int_rand(2,3)
#define DESERT_POINTS   int_rand(2,3)
```

**Go Implementation:** Need to verify if these are:
- Stored as constants (min/max pairs)?
- Computed at planet generation time?
- Checked in `model/planet.go` or `internal/game/planet.go`?

#### 2. **VICT_* Constants** — Victory Calculation Weights
```c
#define VICT_SECT   1000
#define VICT_SHIP    333
#define VICT_TECH    .10
#define VICT_MORALE  200
#define VICT_RES     100
#define VICT_FUEL     15
#define VICT_MONEY     5
#define VICT_DIVISOR 10000
```

**Status:** ❌ **NOT in config.go** — These are missing and should be added.

### ❌ MISSING: Constants Not Yet Ported

| C Name | Purpose | Recommended Action |
|--------|---------|-------------------|
| `MAX_X`, `MAX_Y` | Planet map dimensions (45×19 grid) | Add to config |
| `RATIOXY` | Aspect ratio for map display (3.70) | Add to config or display module |
| `LIMITED_RESOURCES` | Feature flag (set to 1) | Feature flag in config |
| `UP_BID` | Market bid increment (0.10) | Add to config |
| `AFV_FUEL_COST` | AFV fuel usage (1.0) | Add to config (for future AFV support) |
| `VICT_SECT`, etc. | Victory point weights | Add to config |
| **Type/Terrain Constants** | (see below) | In model package |
| **Display Characters** | (see below) | In display/rendering layer |

### 📍 TYPE & TERRAIN CONSTANTS (Not in config, but needed elsewhere)

These are used for planet generation and terrain rendering:

```go
// From tweakables.h — should verify these are in model/
const (
  TYPE_EARTH     = 0
  TYPE_ASTEROID  = 1
  TYPE_MARS      = 2
  TYPE_ICEBALL   = 3
  TYPE_GASGIANT  = 4
  TYPE_WATER     = 5
  TYPE_FOREST    = 6
  TYPE_DESERT    = 7

  SEA     = 0
  LAND    = 1
  MOUNT   = 2
  GAS     = 3
  ICE     = 4
  FOREST  = 5
  DESERT  = 6
  PLATED  = 7
  WASTED  = 8
)

// Display characters — for map rendering
const (
  CHAR_LAND        = '*'
  CHAR_SEA         = '.'
  CHAR_MOUNT       = '^'
  CHAR_DIFFOWNED   = '?'
  CHAR_PLATED      = 'o'
  CHAR_WASTED      = '%'
  CHAR_GAS         = '~'
  CHAR_CLOAKED     = ' '
  CHAR_ICE         = '#'
  CHAR_CRYSTAL     = 'x'
  CHAR_DESERT      = '-'
  CHAR_FOREST      = ')'
)
```

**Action:** Verify these exist in the appropriate packages (`model/`, display layer).

---

## Issues & Recommendations

### High Priority

1. **Add Missing Victory Constants** 
   - VICT_SECT, VICT_SHIP, VICT_TECH, VICT_MORALE, VICT_RES, VICT_FUEL, VICT_MONEY, VICT_DIVISOR
   - Location: `config.go` section for Victory
   - **Impact:** Victory point calculations may be incorrect

2. **Add Planet Map Dimensions**
   - MAX_X (45), MAX_Y (19), RATIOXY (3.70)
   - Location: `config.go` or `model/planet.go`
   - **Impact:** Map generation and display

3. **Verify Planet Victory Points**
   - Check if EARTH_POINTS, ASTEROID_POINTS, etc. are correctly computed
   - Search for where planet victory points are assigned

### Medium Priority

4. **Add Commodity Market Constants**
   - UP_BID (0.10) — needed for market bidding logic
   - Location: `config.go` Market section
   - **Impact:** Market mechanics

5. **Verify Type/Terrain Constants**
   - Confirm TYPE_* and terrain type constants are in `model/` package
   - Check that display character constants are in rendering code

### Low Priority

6. **AFV Support**
   - AFV_FUEL_COST is referenced but AFV system not yet ported
   - Can be deferred until AFV support is added

---

## Verification Checklist

- [ ] **Victory points calculation**: Verify VICT_* constants used correctly in `victory.go`
- [ ] **Planet victory points**: Check that planet type generates correct point values
- [ ] **Map dimensions**: Confirm MAX_X (45) and MAX_Y (19) are used for planet grids
- [ ] **Market bidding**: Verify UP_BID (0.10) used for bid increment logic
- [ ] **Type constants**: Locate and verify TYPE_* (EARTH, ASTEROID, etc.) in code
- [ ] **Terrain constants**: Locate and verify SEA, LAND, MOUNT, GAS, ICE, FOREST, DESERT, PLATED, WASTED
- [ ] **Display characters**: Locate and verify CHAR_* constants for map rendering
- [ ] **All Default() values**: Run spot checks on random values in production pipeline

---

## Next Steps

1. **Phase 1: Add Missing Victory Constants** (30 min)
   - Add VICT_* fields to config.go
   - Update Default() with values
   - Verify victory.go uses these constants

2. **Phase 2: Verify Map Dimensions & Generation** (1 hr)
   - Add MAX_X, MAX_Y, RATIOXY to config
   - Check planet generation code for correct dimensions
   - Test map rendering

3. **Phase 3: Test Production Pipeline** (1-2 hrs)
   - Run production calculations through several update cycles
   - Verify output matches C version
   - Test edge cases (population overflow, resource capping, etc.)

