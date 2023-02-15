# gopp — Postfix policy server in Go

## gopp build and install

Build and install is a simple process:
```bash
make
sudo make install
```
to build gopp and install it into /usr/local/sbin directory

If your use Debian/Ubuntu you also need to copy defaults to /etc:
```bash
cp scripts/gopp-etc_default /etc/defaults/gopp
```
and then
```bash
sudo cp scripts/gopp.service /etc/systemd/system/gopp.service
sudo chmod 664 /etc/systemd/system/gopp.service
sudo systemctl daemon-reload
sudo systemctl enable gopp
sudo systemctl start gopp
```

## Postfix configuration
Edit /etc/postfix/main.cf and append `check_policy_service` to the `smtpd_recipient_restrictions` checklist. Please note `check_policy_service` should be one of the late element of the `smtpd_recipient_restrictions` list, for example:
```
smtpd_recipient_restrictions = permit_mynetworks
        permit_sasl_authenticated
        reject_unauth_destination
        check_policy_service inet:127.0.0.1:10033
```
