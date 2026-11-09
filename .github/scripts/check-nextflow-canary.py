#!/usr/bin/env python3
import csv
import sys

EXPECTED_PROCESSES = {
    "TEST_SUCCESS", "TEST_CREATE_FILE", "TEST_CREATE_EMPTY_FILE", "TEST_CREATE_FOLDER",
    "TEST_INPUT", "TEST_BIN_SCRIPT", "TEST_PASS_FILE", "TEST_PASS_FOLDER",
    "TEST_PUBLISH_FILE", "TEST_PUBLISH_FOLDER", "TEST_IGNORED_FAIL", "TEST_MV_FILE",
    "TEST_MV_FOLDER_CONTENTS", "TEST_VAL_INPUT",
}


def main():
    with open(sys.argv[1], encoding="utf-8", newline="") as trace:
        rows = list(csv.DictReader(trace, delimiter="\t"))
    processes = {row["name"].rsplit(":", 1)[-1].split()[0]: row for row in rows}
    if len(rows) != len(EXPECTED_PROCESSES) or set(processes) != EXPECTED_PROCESSES:
        raise SystemExit(
            f"Unexpected canary coverage: {len(rows)} rows; "
            f"missing={sorted(EXPECTED_PROCESSES - processes.keys())}; "
            f"extra={sorted(processes.keys() - EXPECTED_PROCESSES)}"
        )
    for name, row in processes.items():
        expected = ("FAILED", "1") if name == "TEST_IGNORED_FAIL" else ("COMPLETED", "0")
        actual = (row["status"], row["exit"])
        if actual != expected:
            raise SystemExit(f"{name}: status/exit={actual}, expected {expected}")
    print("All 14 canary processes verified, including the intentional ignored failure.")


if __name__ == "__main__":
    main()
