# Constant Usage Verification — Fixed

**Date:** 2026-06-28  
**Status:** ✅ **CRITICAL ISSUES FIXED**

---

## Summary

Found and fixed 2 critical hardcoding issues in the Go port:

### ✅ Fix #1: RatioXY (Planet Map Aspect Ratio)

**Problem:** Code was using `const ratioXY = 2.6` instead of config value 3.70

**Impact:** CRITICAL — All planets generated with wrong aspect ratio (42% wider than intended)

**Files Changed:**
1. `cmd/makeuniv/main.go`
   - Added config import
   - Added global `cfg` variable (type `*config.Config`)
   - Initialized cfg in main(): `cfg = config.Default()`
   - Changed line 413 from `f := float64(p.MaxX) / ratioXY` to `f := float64(p.MaxX) / cfg.RatioXY`
   - Removed hardcoded `const ratioXY = 2.6` (replaced with comment)

**Before:**
```go
const ratioXY = 2.6
// In makePlanet():
f := float64(p.MaxX) / ratioXY  // 2.6 — WRONG VALUE!
```

**After:**
```go
// In main():
cfg = config.Default()  // Loads RatioXY = 3.70 from config

// In makePlanet():
f := float64(p.MaxX) / cfg.RatioXY  // 3.70 — CORRECT!
```

---

### ✅ Fix #2: UpBid (Market Bid Increment)

**Problem:** Code was using hardcoded `const upBid = 0.10` instead of config value

**Impact:** IMPORTANT — Market bidding ignores config value

**Files Changed:**
1. `internal/game/build.go`
   - Removed hardcoded `const upBid = 0.10`
   - Changed line 2027 from `minbid := int(float64(c.Bid) * (1.0 + upBid))` to use config
   - Added comment explaining upBid is now from config

**Before:**
```go
const upBid = 0.10  // Hardcoded constant

// In cmdBid():
minbid := int(float64(c.Bid) * (1.0 + upBid))  // Uses hardcoded value
```

**After:**
```go
// In cmdBid():
minbid := int(float64(c.Bid) * (1.0 + s.cfg.UpBid))  // Uses config value
```

---

## Verification Status

### ✅ Constants Now Using Config
- **RatioXY** — Planet generation uses config value (3.70)
- **UpBid** — Market bidding uses config value (0.10)

### ⚠️ Still Unused (Deferred)
- **VICT_* constants** — Defined in config but victory scoring not implemented (matches C source)
- **MaxX/MaxY** — Config values added but logic remains per-planet (research needed on intended use)

---

## Next Steps

### Immediate (Testing)
- [ ] Compile and test makeuniv with new RatioXY
- [ ] Generate test universe and verify planet dimensions
- [ ] Test market bidding with different bid increments

### Short-term (Verification)
- [ ] Verify planets generate correctly with 3.70 ratio
- [ ] Compare planet dimensions to C version output
- [ ] Verify market bid logic works as expected

### Optional (Future)
- [ ] Implement victory score calculation (VICT_* constants)
- [ ] Research and clarify MaxX/MaxY global limits

---

## Impact Assessment

### Fixes Applied
- **RatioXY fix:** Corrects fundamental planet generation bug
  - Before: All planets ~42% wider than intended (wrong 2.6 ratio)
  - After: Planets use correct 3.70 ratio from tweakables.h
  - Impact: HIGH — Affects all game mechanics that depend on map layout

- **UpBid fix:** Allows market dynamics to be configured
  - Before: Market bid increment always 0.10, can't be changed
  - After: Configurable via config.json, can tune economy
  - Impact: MEDIUM — Better game balance flexibility

---

## Files Modified

1. **go/cmd/makeuniv/main.go** (4 changes)
   - Added `"github.com/gbgame/gb/internal/config"` import
   - Added `cfg *config.Config` global variable
   - Initialize cfg in main()
   - Use cfg.RatioXY in planet generation

2. **go/internal/game/build.go** (2 changes)
   - Removed `upBid = 0.10` constant
   - Use `s.cfg.UpBid` in cmdBid()

3. **go/internal/config/config.go** (No changes needed)
   - Already has RatioXY = 3.70
   - Already has UpBid = 0.10

---

## Compilation Status

✅ Ready to compile:
```bash
cd /home/steve/workspace/GB/go
go build ./cmd/makeuniv  # Test planet generation fix
go build ./cmd/gb-server  # Test market bidding fix
```

---

## Testing Checklist

- [ ] `go build ./cmd/makeuniv` compiles without errors
- [ ] `go build ./cmd/gb-server` compiles without errors
- [ ] Generate test universe: `./makeuniv`
- [ ] Check planet dimensions in generated database
- [ ] Start server: `./gb-server`
- [ ] Test market: Create commodity lot, place bids
- [ ] Verify bid increments follow UpBid config value

