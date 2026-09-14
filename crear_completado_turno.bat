@echo off
set PGPASSWORD=rodrigo

echo === Agregando campo de completado a turnos ===
C:\pgsql\bin\psql.exe -U postgres -h localhost -p 5432 -d backendauren -c "
ALTER TABLE turnos
    ADD COLUMN IF NOT EXISTS completado_en TIMESTAMPTZ;
"

echo === Verificando ===
C:\pgsql\bin\psql.exe -U postgres -h localhost -p 5432 -d backendauren -c "\d turnos"

echo === Listo! ===
pause