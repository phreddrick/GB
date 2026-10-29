/*
 * stubs.c: link-time stand-ins for symbols that the reference translation
 * units linked into gb_golden reference but never execute during fixture
 * generation (file I/O, notification, turn-driver callbacks, ...).
 *
 * Every function stub aborts with exit status 99. If a fixture generator
 * ever reaches one of these, the run fails loudly instead of producing a
 * fixture that silently depends on stubbed behaviour. Regenerate this list
 * from the linker's 'undefined reference' output when REF_SRCS changes.
 */
#include <stdio.h>
#include <stdlib.h>
#define EXTERN extern
#include "vars.h"
#include "ships.h"
#undef EXTERN

/* data symbols defined in files not linked here (GB_server.c, files.c) */
char *Desnames[] = { 0 };
char Dessymbols[] = "";
char *Files[] = { 0 };
int ShipVector[NUMSTYPES];

static void gb_stub_hit(const char *name)
{
    fprintf(stderr, "gb_golden: reference code called stub %s(); the fixture would depend on stubbed behaviour\n", name);
    exit(99);
}

void adjacent(void) { gb_stub_hit("adjacent"); }
void adjust_morale(void) { gb_stub_hit("adjust_morale"); }
void clr_commodfree(void) { gb_stub_hit("clr_commodfree"); }
void clr_shipfree(void) { gb_stub_hit("clr_shipfree"); }
void compute_power_blocks(void) { gb_stub_hit("compute_power_blocks"); }
void control(void) { gb_stub_hit("control"); }
void Dispshiploc(void) { gb_stub_hit("Dispshiploc"); }
void doabm(void) { gb_stub_hit("doabm"); }
void docked(void) { gb_stub_hit("docked"); }
void domass(void) { gb_stub_hit("domass"); }
void domine(void) { gb_stub_hit("domine"); }
void domissile(void) { gb_stub_hit("domissile"); }
void doown(void) { gb_stub_hit("doown"); }
void doplanet(void) { gb_stub_hit("doplanet"); }
void doship(void) { gb_stub_hit("doship"); }
void getcommod(void) { gb_stub_hit("getcommod"); }
void getdeadcommod(void) { gb_stub_hit("getdeadcommod"); }
void getdeadship(void) { gb_stub_hit("getdeadship"); }
void getplanet(void) { gb_stub_hit("getplanet"); }
void getsdata(void) { gb_stub_hit("getsdata"); }
void getsector(void) { gb_stub_hit("getsector"); }
void getship(void) { gb_stub_hit("getship"); }
void getsmap(void) { gb_stub_hit("getsmap"); }
void getstar(void) { gb_stub_hit("getstar"); }
void insert_sh_plan(void) { gb_stub_hit("insert_sh_plan"); }
void insert_sh_ship(void) { gb_stub_hit("insert_sh_ship"); }
void insert_sh_star(void) { gb_stub_hit("insert_sh_star"); }
void insert_sh_univ(void) { gb_stub_hit("insert_sh_univ"); }
void kill_ship(void) { gb_stub_hit("kill_ship"); }
void landed(void) { gb_stub_hit("landed"); }
void laser_on(void) { gb_stub_hit("laser_on"); }
void makecommoddead(void) { gb_stub_hit("makecommoddead"); }
void makeshipdead(void) { gb_stub_hit("makeshipdead"); }
void moveplanet(void) { gb_stub_hit("moveplanet"); }
void notify(void) { gb_stub_hit("notify"); }
void notify_race(void) { gb_stub_hit("notify_race"); }
void notify_star(void) { gb_stub_hit("notify_star"); }
void Numcommods(void) { gb_stub_hit("Numcommods"); }
void Numships(void) { gb_stub_hit("Numships"); }
void post(void) { gb_stub_hit("post"); }
void push_telegram(void) { gb_stub_hit("push_telegram"); }
void push_telegram_race(void) { gb_stub_hit("push_telegram_race"); }
void Putblock(void) { gb_stub_hit("Putblock"); }
void putcommod(void) { gb_stub_hit("putcommod"); }
void putplanet(void) { gb_stub_hit("putplanet"); }
void Putpower(void) { gb_stub_hit("Putpower"); }
void putrace(void) { gb_stub_hit("putrace"); }
void putsdata(void) { gb_stub_hit("putsdata"); }
void putsector(void) { gb_stub_hit("putsector"); }
void putship(void) { gb_stub_hit("putship"); }
void putsmap(void) { gb_stub_hit("putsmap"); }
void putstar(void) { gb_stub_hit("putstar"); }
void retal_strength(void) { gb_stub_hit("retal_strength"); }
void testship(void) { gb_stub_hit("testship"); }
void use_destruct(void) { gb_stub_hit("use_destruct"); }
void use_fuel(void) { gb_stub_hit("use_fuel"); }
