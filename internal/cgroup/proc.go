package cgroup

import "bytes"

// ScanProcCgroupLine splits procfs membership records at LF without removing a
// trailing CR, which is a legal byte in a cgroup directory name. bufio.ScanLines
// would interpret that byte as part of a CRLF line ending and alter the path.
func ScanProcCgroupLine(data []byte, atEOF bool) (int, []byte, error) {
	if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
		return newline + 1, data[:newline], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
