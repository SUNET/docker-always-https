#!/bin/sh

rm -f /etc/apache2/sites-available/*
rm -f /etc/apache2/sites-enabled/*

# optionally ship acme challenges to an external tool

if [ ! -z "${ACME_URL}" ]; then

cat>/etc/apache2/conf-available/acme.conf<<EOF
ProxyPass /.well-known/acme-challenge ${ACME_URL}/.well-known/acme-challenge/
ProxyPassReverse /.well-known/acme-challenge ${ACME_URL}/.well-known/acme-challenge/
EOF
a2enconf acme
a2enmod proxy proxy_http

fi


if [ -z "${URLS}" ]; then

# In this mode we only redirect to https on $self

   echo "ok" > /var/www/_lvs.txt
   cat>/etc/apache2/sites-available/default.conf<<EOF
<VirtualHost *:80>
       ServerAdmin noc@sunet.se
       ServerName localhost
       DocumentRoot /var/www/

       RewriteEngine On
       RewriteCond %{HTTPS} off
       RewriteRule !(_lvs.txt|.well-known/acme-challenge.*)$ https://%{HTTP_HOST}%{REQUEST_URI} [R=301]
</VirtualHost>
EOF

a2ensite default

else

## In this mode we redirect everything on https on an external URL
## while preserving the query parameters
## http

   cat>/etc/apache2/sites-available/default.conf<<EOF
<VirtualHost *:80>
       ServerAdmin noc@sunet.se
       DocumentRoot /var/www/

       RewriteEngine On
       RewriteCond %{HTTPS} off
       RewriteRule !_lvs.txt$ https://%{HTTP_HOST}%{REQUEST_URI} [R=301]
</VirtualHost>
EOF

## https

KEYDIR=/etc/ssl
mkdir -p $KEYDIR
export KEYDIR

if [ ! -f "$KEYDIR/private/${HOSTNAME}.key" -o ! -f "$KEYDIR/certs/${HOSTNAME}.crt" ]; then
   make-ssl-cert generate-default-snakeoil --force-overwrite
   cp /etc/ssl/private/ssl-cert-snakeoil.key "$KEYDIR/private/${HOSTNAME}.key"
   cp /etc/ssl/certs/ssl-cert-snakeoil.pem "$KEYDIR/certs/${HOSTNAME}.crt"
fi

CHAINSPEC=""
export CHAINSPEC
if [ -f "$KEYDIR/certs/${HOSTNAME}.chain" ]; then
   CHAINSPEC="SSLCertificateChainFile $KEYDIR/certs/${HOSTNAME}.chain"
elif [ -f "$KEYDIR/certs/${HOSTNAME}-chain.crt" ]; then
   CHAINSPEC="SSLCertificateChainFile $KEYDIR/certs/${HOSTNAME}-chain.crt"
elif [ -f "$KEYDIR/certs/${HOSTNAME}.chain.crt" ]; then
   CHAINSPEC="SSLCertificateChainFile $KEYDIR/certs/${HOSTNAME}.chain.crt"
elif [ -f "$KEYDIR/certs/chain.crt" ]; then
   CHAINSPEC="SSLCertificateChainFile $KEYDIR/certs/chain.crt"
elif [ -f "$KEYDIR/certs/chain.pem" ]; then
   CHAINSPEC="SSLCertificateChainFile $KEYDIR/certs/chain.pem"
fi

   cat>/etc/apache2/sites-available/default-ssl.conf<<EOF
<VirtualHost *:443>
       ServerAdmin noc@sunet.se
       DocumentRoot /var/www/

       ServerName ${HOSTNAME}
       SSLProtocol All -SSLv2 -SSLv3
       SSLCompression Off
       SSLCipherSuite "EECDH+ECDSA+AESGCM EECDH+aRSA+AESGCM EECDH+ECDSA+SHA384 EECDH+ECDSA+SHA256 EECDH+aRSA+SHA384 EECDH+aRSA+SHA256 EECDH+AESGCM EECDH EDH+AESGCM EDH+aRSA HIGH !MEDIUM !LOW !aNULL !eNULL !LOW !RC4 !MD5 !EXP !PSK !SRP !DSS"
       SSLEngine On
       SSLCertificateFile $KEYDIR/certs/${HOSTNAME}.crt
       ${CHAINSPEC}
       SSLCertificateKeyFile $KEYDIR/private/${HOSTNAME}.key

       RewriteEngine On
EOF
   for map in ${URLS}; do
      from=`echo $map | awk -F% '{print $1}'`
      to=`echo $map | awk -F% '{print $2}'`
      echo "       RewriteRule ^$from\$ $to%{QUERY_STRING} [R=301,END]" >> /etc/apache2/sites-available/default-ssl.conf
   done
cat>>/etc/apache2/sites-available/default-ssl.conf<<EOF
</VirtualHost>
EOF

a2enmod ssl
a2ensite default-ssl
a2ensite default
fi

mkdir -p /var/log/apache2 /var/lock/apache2 /var/run/apache2
chown -R www-data:www-data /var/log/apache2 /var/lock/apache2 /var/run/apache2

rm -f /var/run/apache2/apache2.pid

env APACHE_LOCK_DIR=/var/lock/apache2 APACHE_RUN_DIR=/var/run/apache2 APACHE_PID_FILE=/var/run/apache2/apache2.pid APACHE_RUN_USER=www-data APACHE_RUN_GROUP=www-data APACHE_LOG_DIR=/var/log/apache2 apache2 -DFOREGROUND
