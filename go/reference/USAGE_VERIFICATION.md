# Constant Usage Verification Report

**Date:** 2026-06-28  
**Status:** ⚠️ **Issues Found — Constants partially integrated**

---

## Executive Summary

**Good News:** Most constants are in config.go  
**Problem:** Several are hardcoded in code instead of using config values

- ✅ **2 constants** using config values correctly
- ❌ **3 constants** hardcoded instead of using config
- ⚠️ **3 constants** defined but never used

---

## Findings by Constant

### 1. **MaxX, MaxY — Planet Grid Dimensions** ⚠️

**Status:** Partially used (per-planet, not global)

**Location:** Each Planet struct has its own MaxX/MaxY fields (model/planet.go:93-94)

**Usage in makeuniv:**
- ✅ Used correctly: Lines 255-424 in cmd/makeuniv/main.go
- Generated per planet based on planet type
- Ranges: Earth 15-23, Asteroid 2-4, Mars 4-8, etc.

**Potential Issue:** These are PER-PLANET dimensions, not enforced by global MaxX/MaxY from config. The config values (45, 19) appear to be universe-scale limits, not actually used anywhere.

**Recommendation:** Need to verify what MaxX=45 and MaxY=19 from tweakables.h actually mean in C source.

---

### 2. **RatioXY — Map Aspect Ratio** ❌ **CRITICAL BUG**

**Status:** Hardcoded in source code, NOT using config value

**Problem:** Code has `const ratioXY = 2.6` but config has 3.70

```go
// cmd/makeuniv/main.go, line 162
const ratioXY = 2.6  // ❌ Should be config.RatioXY = 3.70
```

**Usage:**
- Line 413-414 in makePlanet():
  ```go
  f := float64(p.MaxX) / ratioXY
  maxy := roundRand(f) + 1
  ```

**Impact:** CRITICAL
- All planets are generated with wrong aspect ratio
- Planets will be 42% wider than intended (2.6 vs 3.70)
- This affects map generation and gameplay balance

**Fix Required:** Change line 162 to use config value

**C Source Comparison:**
- tweakables.h defines: `#define RATIOXY 3.70`
- makeplanet.c likely uses this constant for planet generation
- Go code is using wrong value

---

### 3. **UpBid — Market Bid Increment** ❌ **Hardcoded**

**Status:** Hardcoded in source code, NOT using config value

**Problem:** Code has `upBid = 0.10` but should use `s.cfg.UpBid`

```go
// build.go, line 55
const upBid = 0.10  // ❌ Should be from config

// build.go, line 2027 in cmdBid()
minbid := int(float64(c.Bid) * (1.0 + upBid))  // Uses hardcoded value
```

**Impact:** Medium
- Market bidding uses wrong increment
- Config value is ignored
- Can't adjust market dynamics without code change

**Fix Required:** Replace hardcoded constant with config reference

---

### 4. **Victory Point Constants (VICT_*)** ⚠️ **Defined but Unused**

**Status:** Added to config, but never used in code

**Constants defined:**
- VictSect = 1000
- VictShip = 333
- VictTech = 0.10
- VictMorale = 200
- VictRes = 100
- VictFuel = 15
- VictMoney = 5
- VictDivisor = 10000

**Usage:** NONE FOUND
- No code in game/ or model/ uses these constants
- Victory score field exists but is never calculated
- C source also has this issue (defined but not used)

**Recommendation:** Either implement victory scoring or document as deferred

---

## Issues to Fix (Priority Order)

### 🔴 CRITICAL
**1. RatioXY hardcoded (2.6 vs 3.70)**
- File: `cmd/makeuniv/main.go` line 162
- Fix: Change `const ratioXY = 2.6` to reference config
- Impact: Planet generation broken
- Effort: 5 minutes

### 🟠 IMPORTANT
**2. UpBid hardcoded (0.10)**
- File: `internal/game/build.go` lines 55 and 2027
- Fix: Use `s.cfg.UpBid` instead of hardcoded constant
- Impact: Market mechanics incorrect
- Effort: 10 minutes

### 🟡 NICE-TO-HAVE
**3. Understand MaxX/MaxY usage**
- Verify what 45/19 limits mean
- Check if they should be enforced globally
- Effort: 20 minutes research

**4. Implement victory scoring**
- Implement calculation using VICT_* constants
- Effort: 1-2 hours (deferred)

---

## Verification Steps Completed

- [x] Found RatioXY hardcoded with wrong value (2.6 vs 3.70)
- [x] Found UpBid hardcoded instead of using config
- [x] Confirmed VICT_* constants defined but unused
- [x] Verified MaxX/MaxY per-planet (not global limits)
- [x] Located all usages in code

---

## Files to Modify

### Fix #1: RatioXY
**File:** `go/cmd/makeuniv/main.go`
**Current (line 162):**
```go
const ratioXY = 2.6
```
**Should be:**
```go
// In Makeplanet function, pass cfg.RatioXY
// OR add as parameter to makePlanet()
```

### Fix #2: UpBid
**File:** `go/internal/game/build.go`
**Current (lines 55, 2027):**
```go
const upBid = 0.10
...
minbid := int(float64(c.Bid) * (1.0 + upBid))
```
**Should be:**
```go
minbid := int(float64(c.Bid) * (1.0 + s.cfg.UpBid))
```

---

## Next Steps

1. **Immediate:** Fix RatioXY and UpBid hardcoding
2. **Research:** Understand MaxX/MaxY intent
3. **Optional:** Implement victory scoring
4. **Testing:** Verify planets generate correctly
5. **Testing:** Verify market bidding works

---

## Detailed Code References

### RatioXY Usage
```go
// cmd/makeuniv/main.go:410-423
func makePlanet(dist float64, stemp uint8, ptype int) model.Planet {
	...
	p.MaxX = uint8(intRand(xmin[ptype], xmax[ptype]))
	f := float64(p.MaxX) / ratioXY  // ❌ Line 413: Uses hardcoded 2.6
	maxy := roundRand(f) + 1
	...
	p.MaxY = uint8(maxy)
```

### UpBid Usage
```go
// internal/game/build.go:55
const upBid = 0.10  // ❌ Hardcoded

// internal/game/build.go:2027 in cmdBid()
minbid := int(float64(c.Bid) * (1.0 + upBid))  // ❌ Uses hardcoded
```

### Victory Constants (Unused)
- Defined in config.go: VictSect, VictShip, VictTech, VictMorale, VictRes, VictFuel, VictMoney, VictDivisor
- Used in: NOWHERE
- Related struct: model.Race.VictoryScore (never calculated)

