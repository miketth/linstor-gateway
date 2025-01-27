## linstor-gateway nfs create

Creates an NFS export

### Synopsis

Creates a highly available NFS export based on LINSTOR and drbd-reactor.
At first it creates a new resource within the LINSTOR system under the
specified name and using the specified resource group.
After that it creates a drbd-reactor configuration to bring up a highly available NFS 
export.

```
linstor-gateway nfs create NAME SERVICE_IP SIZE [flags]
```

### Examples

```
linstor-gateway nfs create example 192.168.211.122/24 2G
linstor-gateway nfs create restricted 10.10.22.44/16 2G --allowed-ips 10.10.0.0/16

```

### Options

```
      --allowed-ips ip-cidr     Set the IP address mask of clients that are allowed access (default 0.0.0.0/0)
  -p, --export-path string      Set the export path, relative to /srv/gateway-exports (default "/")
  -h, --help                    help for create
  -r, --resource-group string   LINSTOR resource group to use (default "DfltRscGrp")
```

### Options inherited from parent commands

```
      --config string     Config file to load (default "/etc/linstor-gateway/linstor-gateway.toml")
  -c, --connect string    LINSTOR Gateway server to connect to (default "http://localhost:8080")
      --loglevel string   Set the log level (as defined by logrus) (default "info")
```

### SEE ALSO

* [linstor-gateway nfs](linstor-gateway_nfs.md)	 - Manages Highly-Available NFS exports

