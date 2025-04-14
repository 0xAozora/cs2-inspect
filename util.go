package inspect

import (
	"net"
	"unsafe"

	"github.com/0xAozora/go-steam"
)

func getTCPConn(client *steam.Client) *net.TCPConn {

	ptr := unsafe.Add(unsafe.Pointer(&client.Conn), uintptr(8)) // Skip Type Info to get pointer of the underlying structure
	ptr = *(*unsafe.Pointer)(ptr)                               // Dereference to get the pointer to net.Conn
	ptr = unsafe.Add(ptr, uintptr(8))                           // Get actual pointer to the TCPConn (Skipping Type Info)
	conn := *(**net.TCPConn)(ptr)

	return conn
}
