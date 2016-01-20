#!/bin/sh

printenv
rm -f /etc/apache2/sites-available/*
rm -f /etc/apache2/sites-enabled/*
cat>/etc/apache2/sites-available/default.conf<<EOF
<VirtualHost *:80>
       ServerAdmin noc@sunet.se
       ServerName localhost
       DocumentRoot /var/www/

       RewriteEngine On
       RewriteCond %{HTTPS} off
       RewriteRule !_lvs.txt$ https://%{HTTP_HOST}%{REQUEST_URI}
</VirtualHost>
EOF

mkdir -p /var/log/apache2 /var/lock/apache2 /var/run/apache2
chown -R www-data:www-data /var/log/apache2 /var/lock/apache2 /var/run/apache2

a2ensite default

rm -f /var/run/apache2/apache2.pid

env APACHE_LOCK_DIR=/var/lock/apache2 APACHE_RUN_DIR=/var/run/apache2 APACHE_PID_FILE=/var/run/apache2/apache2.pid APACHE_RUN_USER=www-data APACHE_RUN_GROUP=www-data APACHE_LOG_DIR=/var/log/apache2 apache2 -DFOREGROUND
