#!/bin/bash

set -e

# Проверяем наличие аргументов
if [ $# -lt 1 ]; then
    echo "Использование: $0 <user@host> [ssh_key_path]"
    echo "Пример: $0 user@192.168.1.100"
    echo "Пример с ключом: $0 user@192.168.1.100 ~/.ssh/id_rsa"
    exit 1
fi

REMOTE_HOST=$1
SSH_KEY=${2:-""}

echo "Развертывание приложения на удаленном сервере: $REMOTE_HOST"

# Определяем SSH команду
if [ -n "$SSH_KEY" ]; then
    SSH_CMD="ssh -i $SSH_KEY $REMOTE_HOST"
    SCP_CMD="scp -i $SSH_KEY"
else
    SSH_CMD="ssh $REMOTE_HOST"
    SCP_CMD="scp"
fi

# Проверяем подключение к серверу
echo "Проверка подключения к серверу..."
if ! $SSH_CMD "echo 'Подключение успешно'" &> /dev/null; then
    echo "Ошибка: Не удалось подключиться к серверу $REMOTE_HOST"
    exit 1
fi

# Получаем IP адрес сервера
SERVER_IP=$(echo $REMOTE_HOST | cut -d'@' -f2 | cut -d':' -f1)
if [[ ! $SERVER_IP =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    # Если это не IP, пытаемся получить IP через hostname
    SERVER_IP=$($SSH_CMD "hostname -I | awk '{print \$1}'" 2>/dev/null || echo $REMOTE_HOST)
fi

echo "IP адрес сервера: $SERVER_IP"

# Копируем файлы проекта на сервер
echo "Копирование файлов проекта..."
PROJECT_DIR=$(pwd)
$SSH_CMD "mkdir -p ~/project-sem-1" || true
$SCP_CMD -r "$PROJECT_DIR"/* $REMOTE_HOST:~/project-sem-1/ || true

# Устанавливаем зависимости и настраиваем БД на удаленном сервере
echo "Установка зависимостей на удаленном сервере..."
$SSH_CMD << 'ENDSSH'
cd ~/project-sem-1

# Устанавливаем Go, если его нет
if ! command -v go &> /dev/null; then
    echo "Установка Go..."
    wget -q https://go.dev/dl/go1.23.3.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf go1.23.3.linux-amd64.tar.gz
    rm go1.23.3.linux-amd64.tar.gz
    export PATH=$PATH:/usr/local/go/bin
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
fi

# Устанавливаем PostgreSQL, если его нет
if ! command -v psql &> /dev/null; then
    echo "Установка PostgreSQL..."
    sudo apt-get update -qq
    sudo apt-get install -y postgresql postgresql-contrib
    sudo systemctl start postgresql
    sudo systemctl enable postgresql
fi

# Настраиваем PostgreSQL
sudo systemctl start postgresql || true
sudo -u postgres psql -c "CREATE USER validator WITH PASSWORD 'val1dat0r';" 2>/dev/null || true
sudo -u postgres psql -c "ALTER USER validator WITH PASSWORD 'val1dat0r';" 2>/dev/null || true
sudo -u postgres psql -c "CREATE DATABASE \"project-sem-1\" OWNER validator;" 2>/dev/null || true
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE \"project-sem-1\" TO validator;" 2>/dev/null || true

# Настраиваем PostgreSQL для подключения
sudo sed -i "s/#listen_addresses = 'localhost'/listen_addresses = 'localhost'/" /etc/postgresql/*/main/postgresql.conf 2>/dev/null || true
sudo sed -i "s/local   all             all                                     peer/local   all             all                                     md5/" /etc/postgresql/*/main/pg_hba.conf 2>/dev/null || true
sudo systemctl restart postgresql || true

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

# Устанавливаем зависимости Go
export PATH=$PATH:/usr/local/go/bin
go mod download
go mod tidy

# Останавливаем старое приложение, если оно запущено
pkill -f "go run main.go" || true
pkill -f "./project_sem" || true

# Компилируем приложение
echo "Компиляция приложения..."
go build -o project_sem main.go

# Запускаем приложение в фоне
echo "Запуск приложения..."
nohup ./project_sem > app.log 2>&1 &

# Ждем запуска
sleep 3

# Проверяем, что приложение запущено
if pgrep -f "./project_sem" > /dev/null; then
    echo "Приложение успешно запущено"
else
    echo "Ошибка: Приложение не запустилось. Проверьте логи: cat ~/project-sem-1/app.log"
    exit 1
fi
ENDSSH

echo "Развертывание завершено!"
echo "$SERVER_IP"
