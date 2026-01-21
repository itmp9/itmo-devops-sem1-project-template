#!/bin/bash

set -e

echo "Установка зависимостей приложения..."

# Проверяем наличие Go
if ! command -v go &> /dev/null; then
    echo "Go не установлен. Установите Go версии 1.23 или выше."
    exit 1
fi

# Устанавливаем зависимости Go
echo "Загрузка зависимостей Go..."
go mod download
go mod tidy

echo "Подготовка базы данных..."

# Проверяем наличие PostgreSQL
if ! command -v psql &> /dev/null; then
    echo "PostgreSQL не установлен. Установите PostgreSQL."
    exit 1
fi

# Проверяем, запущен ли PostgreSQL
if ! pg_isready -h localhost -p 5432 &> /dev/null; then
    echo "PostgreSQL не запущен на порту 5432. Запустите PostgreSQL."
    exit 1
fi

# Создаем пользователя и базу данных, если их нет
PGPASSWORD=postgres psql -h localhost -U postgres -c "CREATE USER validator WITH PASSWORD 'val1dat0r';" 2>/dev/null || true
PGPASSWORD=postgres psql -h localhost -U postgres -c "ALTER USER validator WITH PASSWORD 'val1dat0r';" 2>/dev/null || true
PGPASSWORD=postgres psql -h localhost -U postgres -c "CREATE DATABASE \"project-sem-1\" OWNER validator;" 2>/dev/null || true
PGPASSWORD=postgres psql -h localhost -U postgres -c "GRANT ALL PRIVILEGES ON DATABASE \"project-sem-1\" TO validator;" 2>/dev/null || true

# Создаем таблицу
PGPASSWORD=val1dat0r psql -h localhost -U validator -d project-sem-1 <<EOF
CREATE TABLE IF NOT EXISTS prices (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    category VARCHAR(255) NOT NULL,
    price DECIMAL(10,2) NOT NULL,
    create_date TIMESTAMP NOT NULL
);
EOF

echo "Подготовка завершена успешно!"
