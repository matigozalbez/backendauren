@echo off
set PGPASSWORD=rodrigo

echo === Creando base de datos backendauren ===
C:\pgsql\bin\psql.exe -U postgres -h localhost -p 5432 -c "CREATE DATABASE backendauren;"

echo === Verificando conexion ===
C:\pgsql\bin\psql.exe -U postgres -h localhost -p 5432 -d backendauren -c "SELECT 1 AS test;"

echo === Listo! ===
pause
