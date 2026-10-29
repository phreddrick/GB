/*
 * gb_golden: C-to-Go golden fixture generator for Galactic Bloodshed (Phase 0).
 *
 * This program links against the ORIGINAL, UNMODIFIED C sources under source/
 * and calls the reference functions directly, writing their outputs as CSV
 * fixtures that the Go golden tests (go/internal/game/golden_test.go) consume.
 * Nothing in source/ is changed; the few symbols the linked C files reference
 * but never execute here are satisfied by stubs.c, and any stub that is
 * actually called aborts the run (exit 99) so a fixture can never silently
 * depend on stubbed behaviour.
 *
 * Randomness: the C code calls libc random() through rand.c. This file
 * defines its own random() (a fixed-seed LCG, see gb_random below) so every
 * run is reproducible, and it counts the calls per fixture row. Two forcing
 * modes (GB_RNG_MIN, GB_RNG_MAX) make round_rand() return floor+1 or floor so
 * that stochastic results can be bracketed by two real C executions. Each CSV
 * row records how the RNG was used ("none", "invariant", "min", "max").
 *
 * Usage: gb_golden <output directory>
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

#define EXTERN            /* this translation unit owns the game globals */
#include "vars.h"
#include "ships.h"
#include "races.h"
#include "power.h"
#include "buffers.h"
#include "doturn.h"
#undef EXTERN

/* Prototypes of the reference functions (from the project's .p files where
 * they exist; the rest are declared here with the signatures used in the .c
 * files). */
#include "shootblast.p"
#include "build.p"
#include "max.p"
#include "rand.p"
#include "doturn.p"
#include "move.p"
#include "dosector.p"
int cew_hit_odds(double, int);
int ship_size(shiptype *);
void Getship(shiptype *, int, racetype *);
void plate(sectortype *);
int governed(racetype *);
double morale_factor(double);

/* ---------------------------------------------------------------- RNG --- */

enum { GB_RNG_LCG = 0, GB_RNG_MIN = 1, GB_RNG_MAX = 2 };
static int gb_rng_mode = GB_RNG_LCG;
static unsigned long gb_rng_state = 20261028UL;
static unsigned long gb_rng_calls = 0;

long random(void)
{
    gb_rng_calls++;
    switch (gb_rng_mode) {
    case GB_RNG_MIN:
        return 0L;                       /* double_rand() == 0.0 */
    case GB_RNG_MAX:
        return 2147483647L;              /* double_rand() == 1 - 2^-31 */
    default:
        gb_rng_state = (gb_rng_state * 1103515245UL + 12345UL) & 0x7fffffffUL;
        if (gb_rng_state == 0)
            gb_rng_state = 1;            /* never hand rand.c an exact 0 */
        return (long)gb_rng_state;
    }
}

static void rng_reset(int mode)
{
    gb_rng_mode = mode;
    gb_rng_calls = 0;
}

static const char *rng_label(const char *invariant_claim)
{
    /* "none": the RNG was not consulted for this row.
     * Otherwise the caller states what it claims about the dependence. */
    if (gb_rng_calls == 0)
        return "none";
    return invariant_claim;
}

/* A separate deterministic sampler for choosing input combinations, so that
 * sampling never disturbs the game RNG call counts. */
static unsigned long samp_state = 12345UL;
static int pick(int n)
{
    samp_state = (samp_state * 6364136223846793005UL + 1442695040888963407UL);
    return (int)((samp_state >> 33) % (unsigned long)n);
}

/* ---------------------------------------------------------------- CSV --- */

static const char *outdir = ".";

static FILE *open_csv(const char *name, const char *header)
{
    char path[1024];
    FILE *f;
    snprintf(path, sizeof path, "%s/%s.csv", outdir, name);
    f = fopen(path, "w");
    if (!f) {
        perror(path);
        exit(2);
    }
    fprintf(f, "%s\n", header);
    return f;
}

static void done_csv(FILE *f, const char *name, int rows)
{
    fclose(f);
    printf("%-18s %5d rows\n", name, rows);
}

/* ----------------------------------------------------- planet_guns ------ */

static void gen_planet_guns(void)
{
    static const int pts[] = { 0, 1, 4, 5, 999, 1000, 1001, 1999, 2000,
                               4999, 5000, 10000, 19999, 20000, 20001, 50000,
                               100000, 1275, 1275000 };
    FILE *f = open_csv("planet_guns", "points,guns,rng");
    int i, n = 0;
    for (i = 0; i < (int)(sizeof pts / sizeof pts[0]); i++) {
        rng_reset(GB_RNG_LCG);
        fprintf(f, "%d,%d,%s\n", pts[i], planet_guns(pts[i]), rng_label("?"));
        n++;
    }
    done_csv(f, "planet_guns", n);
}

/* ----------------------------------------------------- hit_odds --------- */

static void gen_hit_odds(void)
{
    static const double ranges[] = { 0.0, 1.0, 25.0, 100.0, 500.0, 2000.0 };
    static const double techs[] = { 0.0, 1.0, 10.0, 50.0, 200.0, 1000.0 };
    static const int fdams[] = { 0, 10, 50, 99 };
    static const int evs[] = { 0, 1, 4, 9 };
    static const int speeds[] = { 0, 1, 5, 9 };
    static const int bodies[] = { 1, 10, 60, 500, 5000 };
    static const int calibers[] = { NONE, LIGHT, MEDIUM, HEAVY };
    static const int defenses[] = { 0, 1, 5, 10 };
    FILE *f = open_csv("hit_odds",
        "range,tech,fdam,fev,tev,fspeed,tspeed,body,caliber,defense,odds,factor,rng");
    int i, n = 0;
    /* a few hand-picked edges, then 240 sampled combinations */
    struct { double r, t; int fd, fe, te, fs, ts, b, c, d; } edge[] = {
        { 0, 0, 0, 0, 0, 0, 0, 1, NONE, 0 },
        { 0, 0, 0, 0, 0, 0, 0, 1, LIGHT, 0 },
        { 10, 5, 0, 0, 0, 0, 0, 10, MEDIUM, 0 },
        { 100, 50, 25, 2, 2, 3, 3, 100, HEAVY, 3 },
        { 1000, 200, 99, 9, 9, 9, 9, 5000, HEAVY, 10 },
        { 50, 20, 0, 0, 0, 0, 0, 30, LIGHT, 10 },
    };
    for (i = 0; i < (int)(sizeof edge / sizeof edge[0]); i++) {
        int factor = -1, odds;
        rng_reset(GB_RNG_LCG);
        odds = hit_odds(edge[i].r, &factor, edge[i].t, edge[i].fd, edge[i].fe,
                        edge[i].te, edge[i].fs, edge[i].ts, edge[i].b,
                        edge[i].c, edge[i].d);
        fprintf(f, "%.17g,%.17g,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%s\n", edge[i].r,
                edge[i].t, edge[i].fd, edge[i].fe, edge[i].te, edge[i].fs,
                edge[i].ts, edge[i].b, edge[i].c, edge[i].d, odds, factor,
                rng_label("?"));
        n++;
    }
    for (i = 0; i < 240; i++) {
        double r = ranges[pick(6)], t = techs[pick(6)];
        int fd = fdams[pick(4)], fe = evs[pick(4)], te = evs[pick(4)];
        int fs = speeds[pick(4)], ts = speeds[pick(4)], b = bodies[pick(5)];
        int c = calibers[pick(4)], d = defenses[pick(4)];
        int factor = -1, odds;
        rng_reset(GB_RNG_LCG);
        odds = hit_odds(r, &factor, t, fd, fe, te, fs, ts, b, c, d);
        fprintf(f, "%.17g,%.17g,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%s\n", r, t, fd,
                fe, te, fs, ts, b, c, d, odds, factor, rng_label("?"));
        n++;
    }
    done_csv(f, "hit_odds", n);
}

/* ----------------------------------------------------- CEW -------------- */

static void gen_cew(void)
{
    static const double dists[] = { 0.0, 1.0, 10.0, 100.0, 250.0, 500.0, 1000.0, 5000.0 };
    static const int ranges[] = { 0, 1, 100, 250, 500, 1000 };
    FILE *f = open_csv("cew_hit_odds", "dist,cew_range,odds,rng");
    FILE *g;
    int i, j, n = 0, m = 0;
    for (i = 0; i < 8; i++)
        for (j = 0; j < 6; j++) {
            rng_reset(GB_RNG_LCG);
            fprintf(f, "%.17g,%d,%d,%s\n", dists[i], ranges[j],
                    cew_hit_odds(dists[i], ranges[j]), rng_label("?"));
            n++;
        }
    done_csv(f, "cew_hit_odds", n);

    /* CEW_hit() rolls success(odds). Only rows whose odds are 100 (dist equals
     * range) or 0 (far outside range) are RNG-invariant; those are emitted.
     * The strength multiplier applied at the call site in shoot_ship_to_ship()
     * (CR-27) is NOT exercised here; see docs/testing/known-defects.md. */
    g = open_csv("cew_hit", "dist,cew_range,odds,hits,rng");
    for (j = 0; j < 6; j++) {
        int odds, hits;
        rng_reset(GB_RNG_LCG);
        odds = cew_hit_odds((double)ranges[j], ranges[j]);
        hits = CEW_hit((double)ranges[j], ranges[j]);
        if (odds == 100) {
            fprintf(g, "%d,%d,%d,%d,%s\n", ranges[j], ranges[j], odds, hits,
                    rng_label("invariant"));
            m++;
        }
        rng_reset(GB_RNG_LCG);
        odds = cew_hit_odds(20000.0, ranges[j]);
        hits = CEW_hit(20000.0, ranges[j]);
        if (odds == 0) {
            fprintf(g, "%d,%d,%d,%d,%s\n", 20000, ranges[j], odds, hits,
                    rng_label("invariant"));
            m++;
        }
    }
    done_csv(g, "cew_hit", m);
}

/* ----------------------------------------------------- compatibility ---- */

static void gen_compatibility(void)
{
    FILE *f = open_csv("compatibility",
        "p0,p1,p2,p3,p4,p5,p6,p7,p8,p9,p10,r0,r1,r2,r3,r4,r5,r6,r7,r8,r9,compat,rng");
    planettype p;
    racetype r;
    int i, k, n = 0;
    for (k = 0; k < 60; k++) {
        memset(&p, 0, sizeof p);
        memset(&r, 0, sizeof r);
        if (k == 0) {
            /* identical conditions, no toxicity: the maximum */
        } else if (k == 1) {
            p.conditions[TEMP] = 200;         /* huge temperature mismatch */
            r.conditions[TEMP] = -200;
        } else if (k == 2) {
            p.conditions[TOXIC] = 100;        /* fully toxic */
        } else {
            for (i = 0; i <= TOXIC; i++)
                p.conditions[i] = (short)(i == TEMP ? pick(201) - 100 : pick(101));
            for (i = 0; i <= OTHER; i++)
                r.conditions[i] = (short)(i == TEMP ? pick(201) - 100 : pick(101));
        }
        rng_reset(GB_RNG_LCG);
        {
            double c = compatibility(&p, &r);
            for (i = 0; i <= TOXIC; i++)
                fprintf(f, "%d,", p.conditions[i]);
            for (i = 0; i <= OTHER; i++)
                fprintf(f, "%d,", r.conditions[i]);
            fprintf(f, "%.17g,%s\n", c, rng_label("?"));
        }
        n++;
    }
    done_csv(f, "compatibility", n);
}

/* ----------------------------------------------------- maxsupport ------- */

static void gen_maxsupport(void)
{
    FILE *f = open_csv("max_support", "likes,eff,fert,condition,compat,toxic,val,rng");
    racetype r;
    sectortype s;
    int k, n = 0;
    for (k = 0; k < 50; k++) {
        double likes, compat;
        int toxic;
        memset(&r, 0, sizeof r);
        memset(&s, 0, sizeof s);
        s.condition = (unsigned char)pick(WASTED + 1);
        likes = (k == 0) ? 0.0 : (double)pick(101) / 100.0;
        s.eff = (unsigned char)pick(101);
        s.fert = (unsigned char)pick(101);
        compat = (double)pick(101);
        toxic = pick(101);
        r.likes[s.condition] = likes;
        rng_reset(GB_RNG_LCG);
        fprintf(f, "%.17g,%d,%d,%d,%.17g,%d,%d,%s\n", likes, s.eff, s.fert,
                s.condition, compat, toxic, maxsupport(&r, &s, compat, toxic),
                rng_label("?"));
        n++;
    }
    done_csv(f, "max_support", n);
}

/* ----------------------------------------------------- ship design ------ */

static void ship_row(FILE *f, const char *kase, shiptype *s)
{
    rng_reset(GB_RNG_LCG);
    s->size = ship_size(s);
    fprintf(f, "%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,"
               "%d,%.17g,%.17g,%.17g,%s\n",
            kase, s->type, s->build_type, s->primary, s->primtype,
            s->secondary, s->sectype, s->max_crew, s->max_resource,
            s->max_fuel, s->max_destruct, s->max_speed, s->max_hanger,
            s->armor, s->hyper_drive.has, s->laser, s->cloak, s->mount, s->cew,
            s->cew_range, s->size, getmass(s), cost(s), complexity(s),
            rng_label("?"));
}

static void gen_ship_design(void)
{
    FILE *f = open_csv("ship_design",
        "case,type,build_type,primary,primtype,secondary,sectype,max_crew,"
        "max_resource,max_fuel,max_destruct,max_speed,max_hanger,armor,hyper,"
        "laser,cloak,mount,cew,cew_range,size,mass,cost,complexity,rng");
    racetype r;
    shiptype s;
    int i, n = 0;
    memset(&r, 0, sizeof r);
    r.Playernum = 1;
    r.God = 0;
    /* every ship type with the Getship() defaults of a non-God race */
    for (i = 0; i < NUMSTYPES; i++) {
        Getship(&s, i, &r);
        ship_row(f, "default", &s);
        n++;
    }
    /* modified designs: each field pushed up or down from the default */
    for (i = 0; i < NUMSTYPES; i += 3) {
        Getship(&s, i, &r);
        s.primary += 2; s.secondary += 1; s.armor += 3;
        s.max_crew += 5; s.max_resource += 20; s.max_fuel += 15;
        s.max_destruct += 7; s.max_speed += 1; s.max_hanger += 2;
        ship_row(f, "upgraded", &s);
        n++;
        Getship(&s, i, &r);
        s.hyper_drive.has = 1; s.laser = 1; s.cloak = 1; s.mount = 1;
        s.cew = 50; s.cew_range = 300;
        ship_row(f, "advantages", &s);
        n++;
        Getship(&s, i, &r);
        if (s.primary) s.primary -= 1;
        if (s.max_crew > 1) s.max_crew -= 1;
        if (s.max_fuel > 5) s.max_fuel -= 5;
        if (s.max_speed > 1) s.max_speed -= 1;
        ship_row(f, "downgraded", &s);
        n++;
    }
    done_csv(f, "ship_design", n);
}

/* ----------------------------------------------------- ground_attack ---- */

static void gen_ground_attack(void)
{
    FILE *f = open_csv("ground_attack",
        "people,what,civ,mil,def1,def2,alikes,dlikes,afighters,amorale,"
        "dfighters,dmorale,astrength,dstrength,rng");
    racetype Race, alien;
    int k, n = 0;
    for (k = 0; k < 40; k++) {
        int people = 1 + pick(500), what = pick(2);
        unsigned short civ = (unsigned short)pick(400), mil = (unsigned short)pick(60);
        unsigned int def1 = pick(5), def2 = pick(5);
        double alikes = (double)pick(101) / 100.0, dlikes = (double)pick(101) / 100.0;
        double astr = 0, dstr = 0;
        int c1 = 0, c2 = 0, c3 = 0;
        int people_in = people;
        unsigned short civ_in, mil_in;
        memset(&Race, 0, sizeof Race);
        memset(&alien, 0, sizeof alien);
        Race.fighters = (unsigned char)(1 + pick(10));
        alien.fighters = (unsigned char)(1 + pick(10));
        Race.morale = (long)(pick(200001) - 100000);
        alien.morale = (long)(pick(200001) - 100000);
        if (civ + mil == 0)
            civ = 1;                      /* keep dstrength non-zero */
        civ_in = civ; mil_in = mil;
        rng_reset(GB_RNG_LCG);
        ground_attack(&Race, &alien, &people, what, &civ, &mil, def1, def2,
                      alikes, dlikes, &astr, &dstr, &c1, &c2, &c3);
        /* Only the two strengths are RNG-independent; casualties are not
         * emitted in Phase 0 (they need the Go RNG to be injectable, 3b). */
        fprintf(f, "%d,%d,%u,%u,%u,%u,%.17g,%.17g,%d,%ld,%d,%ld,%.17g,%.17g,%s\n",
                people_in, what, civ_in, mil_in, def1, def2, alikes, dlikes,
                Race.fighters, Race.morale, alien.fighters, alien.morale,
                astr, dstr, rng_label("strengths-only"));
        n++;
    }
    done_csv(f, "ground_attack", n);
}

/* ----------------------------------------------------- APadd ------------ */

static void gen_apadd(void)
{
    static const int shs[] = { 0, 1, 7, 10, 15, 20, 30, 50, 100, 199, 200, 400, 1000 };
    static const int popns[] = { 0, 9, 99, 1000, 123456 };
    FILE *f = open_csv("ap_add", "sh,popn,governed,aps_lo,aps_hi,rng");
    racetype r;
    shiptype gov;
    shiptype *shiparr[2];
    int i, j, g, n = 0;
    memset(&gov, 0, sizeof gov);
    gov.alive = 1;
    gov.docked = 1;
    gov.whatdest = LEVEL_PLAN;
    shiparr[0] = NULL;
    shiparr[1] = &gov;
    ships = shiparr;
    Num_ships = 1;
    for (g = 0; g < 2; g++)
        for (i = 0; i < 13; i++)
            for (j = 0; j < 5; j++) {
                int lo, hi;
                memset(&r, 0, sizeof r);
                r.Gov_ship = g ? 1 : 0;
                /* round_rand() under forced RNG: MIN mode rounds every value
                 * up (floor+1), MAX mode rounds down (floor). The Go result
                 * must lie within [aps_lo, aps_hi]. */
                rng_reset(GB_RNG_MAX);
                lo = APadd(shs[i], popns[j], &r);
                rng_reset(GB_RNG_MIN);
                hi = APadd(shs[i], popns[j], &r);
                fprintf(f, "%d,%d,%d,%d,%d,%s\n", shs[i], popns[j], g, lo, hi,
                        "bracket");
                n++;
            }
    ships = NULL;
    Num_ships = 0;
    done_csv(f, "ap_add", n);
}

/* ----------------------------------------------------- produce ---------- */

static void gen_produce(void)
{
    /* Steady-state sectors chosen so that every branch of produce() is
     * either not taken or RNG-invariant: no resource, no crystals,
     * mobilization already at target, efficiency 100 (plating), fertility
     * 100, population exactly at maxsupport (the race likes the plated
     * condition as much as the original one, so plating does not change
     * maxsupport). Rows report rng=none when random() was never called. */
    FILE *f = open_csv("produce",
        "condition,type,mob,popn,troops,tax,likes,likes_plated,metabolism,birthrate,sexes,"
        "fertilize,compat,toxic,eff_out,condition_out,popn_out,owner_out,"
        "avg_mob,maintain,prod_res,prod_fuel,prod_destruct,prod_crystals,rng");
    startype st;
    planettype pl;
    sectortype s;
    racetype r;
    int k, n = 0;
    for (k = 0; k < 12; k++) {
        int cond = (k % 3 == 0) ? GAS : (k % 3 == 1) ? LAND : SEA;
        int mob = pick(101);
        int troops = (k % 2) ? pick(50) : 0;
        int maxsup;
        if (k == 11)
            troops = 0;                   /* popn 0 and no troops: sector is abandoned */
        memset(&st, 0, sizeof st);
        memset(&pl, 0, sizeof pl);
        memset(&s, 0, sizeof s);
        memset(&r, 0, sizeof r);
        races[0] = &r;
        Num_races = 1;
        r.Playernum = 1;
        r.likes[cond] = 1.0;
        r.likes[PLATED] = 1.0;        /* plate() runs first; keep maxsupport stable */
        r.metabolism = 1.0 + (double)pick(300) / 100.0;
        r.birthrate = 0.25 + (double)pick(75) / 100.0;
        r.number_sexes = (unsigned char)(1 + pick(4));
        r.fertilize = 0;
        s.owner = 1;
        s.race = 1;
        s.type = (unsigned char)cond;
        s.condition = (unsigned char)cond;
        s.eff = 100;
        s.fert = 100;
        s.mobilization = (unsigned char)mob;
        s.resource = 0;
        s.crystals = 0;
        s.troops = (unsigned short)troops;
        pl.info[0].mob_set = (unsigned char)mob;
        pl.info[0].tax = (unsigned char)pick(101);
        pl.info[0].resource = 100;
        pl.conditions[TOXIC] = 0;
        Compat[0] = 100.0;
        maxsup = maxsupport(&r, &s, Compat[0], pl.conditions[TOXIC]);
        s.popn = (unsigned short)((k == 11) ? 0 : maxsup);
        prod_res[0] = prod_fuel[0] = prod_destruct[0] = prod_crystals[0] = 0;
        prod_mob = 0;
        avg_mob[0] = 0;
        rng_reset(GB_RNG_LCG);
        produce(&st, &pl, &s);
        fprintf(f, "%d,%d,%d,%d,%d,%d,%.17g,%.17g,%.17g,%.17g,%d,%d,%.17g,%d,"
                   "%d,%d,%d,%d,%lu,%lu,%lu,%lu,%lu,%lu,%s\n",
                cond, cond, mob, (k == 11) ? 0 : maxsup, troops,
                pl.info[0].tax, r.likes[cond], r.likes[PLATED], r.metabolism, r.birthrate,
                r.number_sexes, r.fertilize, Compat[0], pl.conditions[TOXIC],
                s.eff, s.condition, s.popn, s.owner, avg_mob[0],
                r.governor[0].maintain, prod_res[0], prod_fuel[0],
                prod_destruct[0], prod_crystals[0], rng_label("invariant"));
        n++;
    }
    races[0] = NULL;
    Num_races = 0;
    done_csv(f, "produce", n);
}

/* ----------------------------------------------------- main ------------- */

int main(int argc, char **argv)
{
    if (argc > 1)
        outdir = argv[1];
    printf("gb_golden: writing fixtures to %s\n", outdir);
    gen_planet_guns();
    gen_hit_odds();
    gen_cew();
    gen_compatibility();
    gen_maxsupport();
    gen_ship_design();
    gen_ground_attack();
    gen_apadd();
    gen_produce();
    return 0;
}
