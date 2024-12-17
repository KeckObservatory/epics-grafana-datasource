```
# Clean install of Ubuntu Server 24.04
apt install ubuntu-desktop
systemctl disable systemd-networkd.service

# Grafana
apt install -y adduser libfontconfig1 musl
wget https://dl.grafana.com/enterprise/release/grafana-enterprise_11.4.0_amd64.deb
dpkg -i grafana-enterprise_11.4.0_amd64.deb

systemctl daemon-reload
systemctl enable grafana-server
systemctl start grafana-server

# verify Grafana is running, check http://localhost:3000

systemctl stop grafana-server

# ---------
# setup grafana user
chsh grafana
# /bin/bash
chown -R grafana /usr/share/grafana


# ----------
# Install Go
wget https://go.dev/dl/go1.23.4.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.23.4.linux-amd64.tar.gz

# Add to /etc/profile
export PATH=$PATH:/usr/local/go/bin

# Add to ~/.profile
export PATH=$PATH:/usr/local/go/bin:~/go/bin

# Test
go version


# ----------
# Install Node
# installs nvm (Node Version Manager)
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash

# download and install Node.js (you may need to restart the terminal)
nvm install 23

# verifies the right Node.js version is in the environment
node -v # should print `v23.4.0`

# verifies the right npm version is in the environment
npm -v # should print `10.9.2`


# ----------
# Install Mage
go install github.com/magefile/mage@latest
mage -init


# ----------
su - grafana
mkdir /var/lib/grafana/plugins
cd /var/lib/grafana/plugins
git clone https://github.com/KeckObservatory/keyword-grafana-datasource

# Build keyword datasource
cd /var/lib/grafana/plugins/keyword-grafana-datasource
npm install
npm run typecheck
npm run dev
# hit Ctrl-C once the screen stops updating

go get github.com/lib/pq
mage -v build:linux

# Modify Grafana to see plugin
vi /etc/grafana/grafana.ini

# locate this line:
;allow_loading_unsigned_plugins
# change it to:
allow_loading_unsigned_plugins = keyword-grafana-datasource

(as root)
systemctl restart grafana-server
```


## Learn more

Below you can find source code for existing app plugins and other related documentation.

- [Basic data source plugin example](https://github.com/grafana/grafana-plugin-examples/tree/master/examples/datasource-basic#readme)
- [`plugin.json` documentation](https://grafana.com/developers/plugin-tools/reference/plugin-json)
- [How to sign a plugin?](https://grafana.com/developers/plugin-tools/publish-a-plugin/sign-a-plugin)
