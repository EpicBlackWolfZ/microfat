#!/usr/bin/env python3
"""Resolve the previous stable release ancestor; never substitute an unrelated baseline."""
import re
import subprocess


def git(*args):
    return subprocess.check_output(['git', *args], text=True).strip()


def main():
    head = git('rev-parse', 'HEAD')
    for tag in git('tag', '--merged', 'HEAD', '--sort=-version:refname').splitlines():
        if not re.fullmatch(r'v\d+\.\d+\.\d+', tag):
            continue
        commit = git('rev-parse', tag + '^{commit}')
        if commit != head:
            print(commit)
            return
    raise SystemExit('no previous stable release ancestor; explicit baseline support is required')


if __name__ == '__main__':
    main()
