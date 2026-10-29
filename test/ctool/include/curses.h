/*
 * Build shim for the golden harness only. build.c and shlmisc.c include
 * <curses.h> but the functions linked into gb_golden use nothing from it,
 * so this empty header lets the unmodified sources compile without the
 * ncurses development package. It is not used by the game build.
 */
