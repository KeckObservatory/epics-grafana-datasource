# EPICS Archiver Appliance Grafana Data Source Backend

This backend plugin provides a Grafana connection to the [SLAC EPICS archiver appliance](https://github.com/slacmshankar/epicsarchiverap).

### Building

Installation on a Fedora/CentOS system should be similar to other Linux installs.  This is not an exhaustive list of steps, your install will vary.

- Install [Grafana 7](https://grafana.com/docs/grafana/latest/installation/rpm/).

- Install [Go](https://golang.org/doc/install).

- Install node and yarn.
```BASH
curl -sL https://rpm.nodesource.com/setup_14.x | sudo bash -
yum makecache
yum install -y nodejs

curl -sL https://dl.yarnpkg.com/rpm/yarn.repo | tee /etc/yum.repos.d/yarn.repo
yum install -y yarn
```

- Clone this git repos to your plugins directory.

- Get the SDK plugin for Go.
```BASH
go get -u github.com/grafana/grafana-plugin-sdk-go
```

- Clone mage into the plugin.
```BASH
git clone https://github.com/magefile/mage
cd mage ; go run bootstrap.go ; cd ..
mage -v
```

- Build the plugin
```BASH
yarn build
mage -v
```

- Allow loading of unsigned plugins in Grafana.
```BASH
vi /etc/grafana/grafana.ini
# Enter a comma-separated list of plugin identifiers to identify plugins that are allowed to be loaded even if they lack a valid signature. 
allow_loading_unsigned_plugins = keck-observatory-keyword-grafana-datasource,keck-observatory-epics-grafana-datasource
```

- Restart Grafana to pick up the plugin.

