"""Contract tests for coverage accounting across build profiles."""
import sys
sys.dont_write_bytecode = True
import tempfile
import unittest
from pathlib import Path
from coverage import merge, meets_threshold


class CoverageTests(unittest.TestCase):
    def profiles(self, *bodies):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        paths = []
        for index, body in enumerate(bodies):
            path = Path(directory.name) / str(index)
            path.write_text(body)
            paths.append(path)
        return paths

    def test_union_counts_shared_blocks_once_and_includes_exclusive_files(self):
        paths = self.profiles(
            'mode: atomic\np/shared.go:1.1,2.1 3 0\np/full.go:1.1,2.1 2 4\n',
            'mode: atomic\np/shared.go:1.1,2.1 3 7\np/minimal.go:1.1,2.1 1 0\n',
        )
        self.assertEqual(merge(paths), 'mode: atomic\np/full.go:1.1,2.1 2 4\n'
                         'p/minimal.go:1.1,2.1 1 0\np/shared.go:1.1,2.1 3 7\n')

    def test_repeated_blocks_from_coverpkg_count_once(self):
        profiles = self.profiles('mode: atomic\np/a.go:1.1,2.1 3 0\np/a.go:1.1,2.1 3 5\n')
        self.assertEqual(merge(profiles), 'mode: atomic\np/a.go:1.1,2.1 3 5\n')

    def test_threshold_does_not_round_up(self):
        self.assertFalse(meets_threshold(94973, 100000, 95))
        self.assertTrue(meets_threshold(95000, 100000, 95))
        self.assertFalse(meets_threshold(0, 0, 95))

    def test_rejects_invalid_or_incompatible_profiles(self):
        good = 'mode: atomic\np/a.go:1.1,2.1 3 1\n'
        for bad in ['mode: set\n', 'mode: atomic\np/a.go:1.1,2.1 4 1\n',
                    'mode: atomic\np/a.go:1.1,3.1 3 1\n',
                    'mode: atomic\np/a.go:1.1,2.1 3 -1\n',
                    good + 'p/a.go:1.1,2.1 4 1\n']:
            with self.subTest(profile=bad), self.assertRaises(ValueError):
                merge(self.profiles(good, bad))
        with self.assertRaises(ValueError):
            merge(self.profiles('mode: atomic\n'))


if __name__ == '__main__':
    unittest.main()
