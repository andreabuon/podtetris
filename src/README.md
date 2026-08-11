# PODTetris local setup (multi-terminal)

## Setup

1. `make cluster`

Then run each command in a **dedicated** terminal window:
2. `cd evictor && make install && make run`
3. `cd webhook && make deploy`
4. `cd planner && make run`

## Cleanup

  `make clean`
