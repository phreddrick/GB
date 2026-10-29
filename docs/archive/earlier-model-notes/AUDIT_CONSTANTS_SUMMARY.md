# Production/Economy Constants Audit — Summary Report

**Date:** 2026-06-28  
**Status:** ✅ **COMPLETE — Constants verified & missing ones added**

---

## Overview

Comprehensive audit of game balance constants from C `tweakables.h` vs. Go `internal/config/config.go`.

**Result:** All critical constants are present and correct.

---

## Actions Taken

### ✅ Added Missing Constants to config.go

The following constants were missing and have been added:

1. **Planet Map Dimensions**
   - `MaxX = 45` — planet grid width
   - `MaxY = 19` — planet grid height  
   - `RatioXY = 3.70` — aspect ratio for map display

2. **Victory Point Weights** (VICT_* from tweakables.h)
   - `VictSect = 1000` — sector victory weight
   - `VictShip = 333` — ship victory weight
   - `VictTech = 0.10` — tech victory weight
   - `VictMorale = 200` — morale victory weight
   - `VictRes = 100` — resource victory weight
   - `VictFuel = 15` — fuel victory weight
   - `VictMoney = 5` — money victory weight
   - `VictDivisor = 10000` — normalization divisor

3. **Commodity Market**
   - `UpBid = 0.10` — bid increment for market bids

---

## Critical Finding: Victory Score Calculation

**Status:** ❌ **NOT IMPLEMENTED** (matches C source behavior)

Both C and Go versions have:
- A `victory_score` field in the race struct
- The VICT_* constants defined
- **BUT:** No actual calculation code that uses these constants

The C source file `victory.c` displays the score but doesn't calculate it. The Go port has identical behavior: the field exists but is never computed.

**Options:**
1. **Implement** — Write victory score calculation using VICT_* constants
2. **Remove** — Delete unused fields and constants
3. **Document** — Mark as "deferred feature"

Current recommendation: **Document as deferred**, implement in Phase 4 if needed.

---

## Verification Status

### ✅ Verified Correct (85+ constants)

All numeric constants in config.go match C values exactly:
- Ship mass/size factors
- Production rates (resource, fuel, destruct, population)
- Combat damage calculations
- Navigation/fuel consumption
- Technology investment
- Population movement thresholds
- Terraforming costs
- Troop/mobilization parameters
- Repair rates
- Merchant/market economics
- Telegram system
- Universe generation

### 📍 Located Elsewhere (Ship Build Costs)

Ship component costs are correctly placed in `build.go:45-53` as private constants (not in config) since they're only used there:
- gunCost = 1.00
- crewCost = 0.05
- cargoCost = 0.05
- fuelCostBld = 0.05
- ammoCost = 0.05
- speedCost = 0.50
- cewCost = 0.003
- armorCost = 3.50
- hangerCost = 0.50

### 🎯 Still Need to Verify

These exist in the code but need verification they're being used correctly:

1. **Max Sector Population** — `MaxSectPopn = 32767` used as cap?
2. **Sector Repair Cost** — `SectorRepairCost = 10` used in doplanet()?
3. **Toxicity Threshold** — `EnvirDamageTox = 70` used in doplanet()?
4. **Population Movement Scales** — PopnMoveScale1/2 used in move logic?
5. **Terrain Type Constants** — Verify TYPE_* constants in model package
6. **Display Characters** — Verify CHAR_* constants in rendering layer

---

## Next Steps

### Immediate (High Priority)
- [ ] Search for MAX_X and MAX_Y usage in planet generation code
- [ ] Verify market bidding uses UP_BID constant
- [ ] Check for hardcoded "45" or "19" in planet code (should use config values)

### Testing (Medium Priority)
- [ ] Run production through several update cycles
- [ ] Verify population dynamics match C version
- [ ] Test edge cases: overflow, starvation, max population
- [ ] Test tech investment calculations

### Optional (Low Priority)
- [ ] Implement victory score calculation
- [ ] Add unit tests for production pipeline
- [ ] Performance profiling of constant lookups

---

## Files Modified

- **[/home/steve/workspace/GB/go/internal/config/config.go](go/internal/config/config.go)**
  - Added 12 new fields to Config struct
  - Updated Default() with 12 new constant values

- **[/home/steve/workspace/GB/go/CONSTANT_AUDIT.md](go/CONSTANT_AUDIT.md)** 
  - Detailed comparison of all 100+ constants
  - Verification status for each
  - Identified missing and misplaced constants

---

## Summary

✅ **Constants audit complete**  
✅ **All critical constants present in config**  
✅ **Missing constants added**  
⚠️ **Victory score calculation deferred (not in C source either)**  
🎯 **Next: Verify usage in production pipeline**
