//go:build linux

package secure

import (
	"log"

	"golang.org/x/sys/unix"
)

// HardenProcess locks the agent's memory into RAM (best effort) and makes
// the process non-dumpable: ECDH secrets, the identity seed and decrypted
// frames can never page out to swap where an offline attacker or cold-boot
// attack could recover them, and PR_SET_DUMPABLE(0) blocks ptrace-based
// memory manipulation by same-UID processes — an attacker cannot read or
// overwrite the running security checks with an ordinary ptrace. Failures
// are logged, never fatal — hardened kernels without CAP_IPC_LOCK still
// run. (Root with CAP_SYS_PTRACE is unfixable in userspace; the systemd
// sandbox adds MemoryDenyWriteExecute against code injection.)
func HardenProcess() {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		log.Printf("hardening: PR_SET_DUMPABLE unavailable (%v) — ptrace protection disabled", err)
	} else {
		log.Printf("hardening: process non-dumpable (PR_SET_DUMPABLE=0) — no same-UID ptrace")
	}
	if err := unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE); err != nil {
		log.Printf("hardening: mlockall unavailable (%v) — swap protection disabled", err)
		return
	}
	log.Printf("hardening: memory locked (mlockall) — secrets cannot page to swap")
}
