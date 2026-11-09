# Builds the golden-fixture generator against the unmodified C sources in
# ../../source and regenerates ../golden/testdata/*.csv.
#
#   make            build gb_golden
#   make fixtures   build and regenerate the CSV fixtures
#   make check      regenerate into a temp dir and diff against the committed fixtures
#   make clean
#
# The C code is K&R-era ANSI C; it compiles with GCC 13 using -std=gnu89 and
# -fcommon (tentative definitions in vars.h). Warnings are suppressed on purpose:
# the reference sources are not ours to clean up.

CC      ?= gcc
SRC     := ../../source
OUT     := ../golden/testdata
CFLAGS  := -std=gnu89 -fcommon -w -O0 -g -I$(SRC) -Iinclude
LDLIBS  := -lm

# Reference translation units that contain the functions under test and the
# helpers they call (rand.c supplies int_rand/round_rand; our random() in
# main.c replaces libc's).
REF_SRCS := shootblast.c build.c max.c dosector.c doturn.c move.c shlmisc.c misc.c rand.c
REF_OBJS := $(patsubst %.c,obj/%.o,$(REF_SRCS))

all: gb_golden gb_racegen_golden

obj:
	mkdir -p obj

obj/%.o: $(SRC)/%.c | obj
	$(CC) $(CFLAGS) -c $< -o $@

obj/main.o: main.c | obj
	$(CC) $(CFLAGS) -c $< -o $@

obj/stubs.o: stubs.c | obj
	$(CC) $(CFLAGS) -c $< -o $@

gb_golden: obj/main.o obj/stubs.o $(REF_OBJS)
	$(CC) $(CFLAGS) -o $@ $^ $(LDLIBS)

obj/racegen_golden.o: racegen.c $(SRC)/racegen.c $(SRC)/racegen.h $(SRC)/game_info.h | obj
	$(CC) $(CFLAGS) -ffunction-sections -fdata-sections -c $< -o $@

obj/GB_racegen_ref.o: $(SRC)/GB_racegen.c | obj
	$(CC) $(CFLAGS) -ffunction-sections -fdata-sections -c $< -o $@

gb_racegen_golden: obj/racegen_golden.o obj/GB_racegen_ref.o
	$(CC) $(CFLAGS) -Wl,--gc-sections -o $@ $^ $(LDLIBS)

fixtures: all
	mkdir -p $(OUT)
	./gb_golden $(OUT)
	./gb_racegen_golden $(OUT)

check: all
	rm -rf obj/check && mkdir -p obj/check
	./gb_golden obj/check
	./gb_racegen_golden obj/check
	diff -r obj/check $(OUT) && echo "fixtures reproduce exactly"

clean:
	rm -rf obj gb_golden gb_racegen_golden

.PHONY: all fixtures check clean
