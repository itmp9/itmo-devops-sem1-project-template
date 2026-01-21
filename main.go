package main

import (
	"archive/tar"
	"archive/zip"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

const (
	dbHost     = "localhost"
	dbPort     = 5432
	dbUser     = "validator"
	dbPassword = "val1dat0r"
	dbName     = "project-sem-1"
	serverPort = 8080
)

type Price struct {
	ID         int
	Name       string
	Category   string
	Price      float64
	CreateDate time.Time
}

type Response struct {
	TotalItems     int     `json:"total_items"`
	TotalCategories int    `json:"total_categories"`
	TotalPrice     float64 `json:"total_price"`
}

var db *sql.DB

func initDB() error {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)

	var err error
	db, err = sql.Open("postgres", psqlInfo)
	if err != nil {
		return fmt.Errorf("ошибка подключения к БД: %v", err)
	}

	if err = db.Ping(); err != nil {
		return fmt.Errorf("ошибка ping БД: %v", err)
	}

	// Создаем таблицу, если её нет
	createTableQuery := `
	CREATE TABLE IF NOT EXISTS prices (
		id SERIAL PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		category VARCHAR(255) NOT NULL,
		price DECIMAL(10,2) NOT NULL,
		create_date TIMESTAMP NOT NULL
	);`

	_, err = db.Exec(createTableQuery)
	if err != nil {
		return fmt.Errorf("ошибка создания таблицы: %v", err)
	}

	return nil
}

func extractZip(r io.Reader, size int64) ([]Price, error) {
	// Создаем временный файл для zip
	tmpFile, err := os.CreateTemp("", "upload-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, r)
	if err != nil {
		return nil, err
	}

	zipReader, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return nil, err
	}
	defer zipReader.Close()

	var prices []Price
	for _, file := range zipReader.File {
		if strings.HasSuffix(file.Name, ".csv") {
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}

			prices, err = parseCSV(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			break
		}
	}

	return prices, nil
}

func extractTar(r io.Reader) ([]Price, error) {
	// Создаем временный файл для tar
	tmpFile, err := os.CreateTemp("", "upload-*.tar")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, r)
	if err != nil {
		return nil, err
	}

	tarFile, err := os.Open(tmpFile.Name())
	if err != nil {
		return nil, err
	}
	defer tarFile.Close()

	tarReader := tar.NewReader(tarFile)
	var prices []Price

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if strings.HasSuffix(header.Name, ".csv") {
			prices, err = parseCSV(tarReader)
			if err != nil {
				return nil, err
			}
			break
		}
	}

	return prices, nil
}

func parseCSV(r io.Reader) ([]Price, error) {
	reader := csv.NewReader(r)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 2 {
		return []Price{}, nil
	}

	var prices []Price
	for i := 1; i < len(records); i++ {
		record := records[i]
		if len(record) < 5 {
			continue
		}

		id, err := strconv.Atoi(strings.TrimSpace(record[0]))
		if err != nil {
			continue
		}

		name := strings.TrimSpace(record[1])
		category := strings.TrimSpace(record[2])

		price, err := strconv.ParseFloat(strings.TrimSpace(record[3]), 64)
		if err != nil {
			continue
		}

		createDate, err := time.Parse("2006-01-02", strings.TrimSpace(record[4]))
		if err != nil {
			continue
		}

		prices = append(prices, Price{
			ID:         id,
			Name:       name,
			Category:   category,
			Price:      price,
			CreateDate: createDate,
		})
	}

	return prices, nil
}

func insertPrices(prices []Price) (int, error) {
	// Открываем транзакцию
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("ошибка начала транзакции: %v", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO prices (name, category, price, create_date) VALUES ($1, $2, $3, $4)`
	insertedCount := 0
	
	// В транзакции вставляем записи
	for _, price := range prices {
		_, err := tx.Exec(query, price.Name, price.Category, price.Price, price.CreateDate)
		if err != nil {
			log.Printf("Ошибка вставки записи: %v", err)
			// Откатываем транзакцию при ошибке
			return 0, fmt.Errorf("ошибка вставки данных: %v", err)
		}
		insertedCount++
	}

	// Коммитим транзакцию
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("ошибка коммита транзакции: %v", err)
	}

	return insertedCount, nil
}

func getStats(insertedCount int) (Response, error) {
	var resp Response

	// total_items - количество в текущей загрузке
	resp.TotalItems = insertedCount
	
	// total_categories - общее количество категорий (по всей БД)
	// total_price - суммарная стоимость всех объектов в базе данных
	query := `
		SELECT 
			COUNT(DISTINCT category) as total_categories,
			COALESCE(SUM(price), 0) as total_price
		FROM prices
	`
	
	err := db.QueryRow(query).Scan(&resp.TotalCategories, &resp.TotalPrice)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

func postPricesHandler(w http.ResponseWriter, r *http.Request) {
	archiveType := r.URL.Query().Get("type")
	if archiveType == "" {
		archiveType = "zip"
	}

	// Получаем файл из multipart form
	err := r.ParseMultipartForm(32 << 20) // 32 MB max
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка парсинга формы: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка получения файла: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	var prices []Price

	switch archiveType {
	case "zip":
		prices, err = extractZip(file, header.Size)
		if err != nil {
			http.Error(w, fmt.Sprintf("Ошибка обработки zip архива: %v", err), http.StatusInternalServerError)
			return
		}
	case "tar":
		prices, err = extractTar(file)
		if err != nil {
			http.Error(w, fmt.Sprintf("Ошибка обработки tar архива: %v", err), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "Неподдерживаемый тип архива. Используйте zip или tar", http.StatusBadRequest)
		return
	}


	// Вставляем данные в БД
	insertedCount, err := insertPrices(prices)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка вставки данных: %v", err), http.StatusInternalServerError)
		return
	}

	// Получаем статистику
	stats, err := getStats(insertedCount)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка получения статистики: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func getPricesHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем все данные из БД
	query := `SELECT id, name, category, price, create_date FROM prices ORDER BY id`
	rows, err := db.Query(query)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка запроса к БД: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Создаем временный CSV файл
	tmpCSV, err := os.CreateTemp("", "data-*.csv")
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка создания временного файла: %v", err), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpCSV.Name())
	defer tmpCSV.Close()

	writer := csv.NewWriter(tmpCSV)
	writer.Write([]string{"id", "name", "category", "price", "create_date"})

	for rows.Next() {
		var id int
		var name, category string
		var price float64
		var createDate time.Time

		err := rows.Scan(&id, &name, &category, &price, &createDate)
		if err != nil {
			http.Error(w, fmt.Sprintf("Ошибка чтения данных: %v", err), http.StatusInternalServerError)
			return
		}

		writer.Write([]string{
			strconv.Itoa(id),
			name,
			category,
			strconv.FormatFloat(price, 'f', 2, 64),
			createDate.Format("2006-01-02"),
		})
	}

	// Проверяем ошибку после цикла rows.Next()
	if err = rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("Ошибка при чтении данных из БД: %v", err), http.StatusInternalServerError)
		return
	}

	writer.Flush()
	if err = writer.Error(); err != nil {
		http.Error(w, fmt.Sprintf("Ошибка записи CSV: %v", err), http.StatusInternalServerError)
		return
	}

	// Создаем zip архив
	tmpZip, err := os.CreateTemp("", "response-*.zip")
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка создания zip: %v", err), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpZip.Name())
	defer tmpZip.Close()

	zipWriter := zip.NewWriter(tmpZip)
	
	csvFile, err := os.Open(tmpCSV.Name())
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка открытия CSV: %v", err), http.StatusInternalServerError)
		return
	}
	defer csvFile.Close()

	zipFile, err := zipWriter.Create("data.csv")
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка создания файла в zip: %v", err), http.StatusInternalServerError)
		return
	}

	_, err = io.Copy(zipFile, csvFile)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка копирования в zip: %v", err), http.StatusInternalServerError)
		return
	}

	err = zipWriter.Close()
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка закрытия zip: %v", err), http.StatusInternalServerError)
		return
	}

	// Отправляем zip файл
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=data.zip")
	
	tmpZip.Seek(0, 0)
	io.Copy(w, tmpZip)
}

func main() {
	err := initDB()
	if err != nil {
		log.Fatalf("Ошибка инициализации БД: %v", err)
	}
	defer db.Close()

	r := mux.NewRouter()
	r.HandleFunc("/api/v0/prices", postPricesHandler).Methods("POST")
	r.HandleFunc("/api/v0/prices", getPricesHandler).Methods("GET")

	log.Printf("Сервер запущен на порту %d", serverPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", serverPort), r))
}
