/* CR-07A fixtures: execute the original cost/normal/metamorph functions.
 * No source/ edits, no C++ code, no calls to the interactive designer/writer.
 * Unused interactive sections are discarded by the linker, not stubbed.
 */
#define main original_racegen_main
#include "../../source/racegen.c"
#undef main
#include <limits.h>

extern int planet_translate[N_HOME_PLANET_TYPES];
static attribute original_attrs[N_ATTRIBUTES];
static FILE *out;

static void defaults(int kind, int home)
{
    int i;
    memcpy(attr, original_attrs, sizeof(original_attrs));
    memset(&race, 0, sizeof(race));
    for (i = 0; i < N_ATTRIBUTES; ++i) race.attr[i] = attr[i].init;
    race.home_planet_type = H_EARTH;
    race.compat[S_PLATED] = 100;
    normal();
    /* initialize():684-687, with its inverted memcpy excluded. The fixture
     * supplies the intended default input explicitly; the cost functions
     * and integer calibration execute unchanged. */
    cost_of_race();
    for (i = 0; i < N_ATTRIBUTES; ++i) attr[i].l_fudge += -cost.attr[i];
    race.race_type = kind;
    race.home_planet_type = home;
    if (kind == R_METAMORPH) metamorph();
}

static void row(const char *name)
{
    int i, total;
    /* All input grids below have <=7 nonzero sectors and bounded int costs.
     * Never ask C for blah[8] or overflow a signed sum. */
    int n = 0;
    for (i = 0; i < N_SECTOR_TYPES; ++i) n += race.compat[i] != 0;
    if (n >= N_SECTOR_TYPES) abort();
    total = cost_of_race();
    fprintf(out, "%s,%d,%d", name, race.race_type, race.home_planet_type);
    for (i = 0; i < N_ATTRIBUTES; ++i) fprintf(out, ",%.17g", race.attr[i]);
    for (i = 0; i < N_SECTOR_TYPES; ++i) fprintf(out, ",%.17g", race.compat[i]);
    for (i = 0; i < N_ATTRIBUTES; ++i) fprintf(out, ",%.0f", cost.attr[i]);
    for (i = 0; i < N_SECTOR_TYPES; ++i) fprintf(out, ",%.0f", cost.compat[i]);
    fprintf(out, ",%d,%d,%d,%d\n", cost.home_planet_type, cost.n_sector_types, total, STARTING_POINTS-total);
}

static FILE *open_output(const char *dir, const char *name)
{
    char path[4096];
    FILE *f;
    if (snprintf(path, sizeof(path), "%s/%s.csv", dir, name) >= sizeof(path)) abort();
    f = fopen(path, "w");
    if (!f) { perror(path); exit(1); }
    return f;
}

int main(int argc, char **argv)
{
    int kind, home, i, j, edge, mask, sample;
    char name[100];
    double rounds[] = {-350.500001,-350.5,-350.499999,-1.500001,-1.5,-1.499999,-.500001,-.5,-.499999,0,.499999,.5,.500001,1.499999,1.5,1.500001};
    unsigned long state = 4242;
    if (argc != 2) return 2;
    memcpy(original_attrs, attr, sizeof(original_attrs));
    out = open_output(argv[1], "racegen_cost");
    fprintf(out, "case,race_type,home");
    for (i=0;i<N_ATTRIBUTES;++i) fprintf(out, ",a%d",i);
    for (i=0;i<N_SECTOR_TYPES;++i) fprintf(out, ",s%d",i);
    for (i=0;i<N_ATTRIBUTES;++i) fprintf(out, ",ac%d",i);
    for (i=0;i<N_SECTOR_TYPES;++i) fprintf(out, ",sc%d",i);
    fprintf(out, ",home_cost,surcharge,total,remaining\n");
    for (kind=0;kind<N_RACE_TYPES;++kind) {
        for (home=0;home<N_HOME_PLANET_TYPES;++home) {
            defaults(kind,home);
            sprintf(name,"default-%d-%d",kind,home); row(name);
        }
        for (i=0;i<N_ATTRIBUTES;++i) for (edge=0;edge<3;++edge) {
            defaults(kind,H_EARTH);
            race.attr[i] = edge==0 ? attr[i].minimum : edge==1 ? attr[i].init : attr[i].maximum;
            sprintf(name,"attribute-%d-%d-%d",kind,i,edge); row(name);
        }
        /* Every pair of traits at extrema: exercises all covariance edges. */
        for (i=0;i<N_ATTRIBUTES;++i) for (j=i+1;j<N_ATTRIBUTES;++j) for (edge=0;edge<4;++edge) {
            defaults(kind,H_EARTH);
            race.attr[i] = edge&1 ? attr[i].maximum : attr[i].minimum;
            race.attr[j] = edge&2 ? attr[j].maximum : attr[j].minimum;
            sprintf(name,"traits-%d-%d-%d-%d",kind,i,j,edge); row(name);
        }
    }
    /* All home/sector multipliers, every sector pair, 0..7-type surcharges. */
    for (home=0;home<N_HOME_PLANET_TYPES;++home) {
        for (i=0;i<N_SECTOR_TYPES;++i) for (edge=0;edge<3;++edge) {
            defaults(R_NORMAL,home); memset(race.compat,0,sizeof(race.compat));
            race.compat[i] = edge==0 ? 0 : edge==1 ? 1 : 100;
            sprintf(name,"sector-%d-%d-%d",home,i,edge); row(name);
        }
        for (i=0;i<N_SECTOR_TYPES;++i) for (j=i+1;j<N_SECTOR_TYPES;++j) {
            defaults(R_NORMAL,home); memset(race.compat,0,sizeof(race.compat));
            race.compat[i]=race.compat[j]=100;
            sprintf(name,"sectors-%d-%d-%d",home,i,j); row(name);
        }
        for (mask=0;mask<128;++mask) {
            defaults(R_NORMAL,home); memset(race.compat,0,sizeof(race.compat));
            for (i=0,j=0;i<N_SECTOR_TYPES;++i) if (i!=S_GAS) race.compat[i]=(mask&(1<<j++))?100:0;
            sprintf(name,"terrain-mask-%d-%d",home,mask); row(name);
        }
    }
    /* Fixed integer LCG for additional mixed, bounded designs. */
    for (sample=0;sample<512;++sample) {
        defaults(sample%2,sample%N_HOME_PLANET_TYPES);
        for (i=0;i<N_ATTRIBUTES;++i) {
            state=(state*1664525UL+1013904223UL)&0xffffffffUL;
            race.attr[i]=attr[i].minimum+(attr[i].maximum-attr[i].minimum)*(state%101)/100.0;
        }
        for (i=0;i<N_SECTOR_TYPES;++i) {
            state=(state*1664525UL+1013904223UL)&0xffffffffUL;
            race.compat[i]=i==S_GAS?0:state%101;
        }
        sprintf(name,"sample-%d",sample); row(name);
    }
    if (fclose(out)) return 1;
    out=open_output(argv[1],"racegen_round");
    fprintf(out,"input,rounded\n");
    for(i=0;i<sizeof(rounds)/sizeof(rounds[0]);++i) fprintf(out,"%.17g,%d\n",rounds[i],ROUND(rounds[i]));
    if(fclose(out)) return 1;
    out=open_output(argv[1],"racegen_metadata");
    fprintf(out,"kind,attribute,name,min,default,max,integral,fudge");
    for(i=0;i<N_ATTRIBUTES;++i) fprintf(out,",cov%d",i);
    fprintf(out,"\n");
    for(kind=0;kind<N_RACE_TYPES;++kind) {
        defaults(kind,H_EARTH);
        for(i=0;i<N_ATTRIBUTES;++i) {
            fprintf(out,"%d,%d,%s,%.17g,%.17g,%.17g,%d,%.0f",kind,i,attr[i].print_name,attr[i].minimum,attr[i].init,attr[i].maximum,attr[i].is_integral,attr[i].l_fudge);
            for(j=0;j<N_ATTRIBUTES;++j) fprintf(out,",%.17g",attr[i].cov[j]);
            fprintf(out,"\n");
        }
    }
    if(fclose(out)) return 1;
    out=open_output(argv[1],"racegen_homeworld");
    fprintf(out,"home,name,planet_type,cost");
    for(i=0;i<N_SECTOR_TYPES;++i) fprintf(out,",cov%d",i);
    fprintf(out,"\n");
    for(home=0;home<N_HOME_PLANET_TYPES;++home) {
        fprintf(out,"%d,%s,%d,%d",home,planet_print_name[home],planet_translate[home],planet_cost[home]);
        for(i=0;i<N_SECTOR_TYPES;++i) fprintf(out,",%.17g",planet_compat_cov[home][i]);
        fprintf(out,"\n");
    }
    return fclose(out)!=0;
}
