```
openssl genrsa -aes256 -passout file:passout -out priv.pem
openssl rsa -in priv.pem -out public.pem -outform PEM -pubout
```
