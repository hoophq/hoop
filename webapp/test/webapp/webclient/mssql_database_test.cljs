(ns webapp.webclient.mssql-database-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.webclient.mssql-database :as mssql-database]))

(def ^:private user-script "SELECT * FROM dbo.t")

;; One line per server language, from `USE [jd-pix]` run through the gateway and agent.
(deftest drops-the-database-context-line
  (doseq [line ["Changed database context to 'jd-pix'."
                "Changed database context to 'jd-pix'.\r"
                "Der Datenbankkontext wurde in \"jd-pix\" geändert."
                "Le contexte de la base de données a été modifié en 'jd-pix'."
                "データベース コンテキストが 'jd-pix' に変更されました。"
                "Databasekonteksten blev ændret til 'jd-pix'."
                "Se cambió el contexto de la base de datos a 'jd-pix'."
                "Il contesto di database è stato sostituito con 'jd-pix'."
                "De databasecontext is gewijzigd in jd-pix."
                "O contexto da base de dados foi alterado para 'jd-pix'."
                "Tietokannan kontekstiksi muutettiin jd-pix."
                "Ändrade databasens kontext till jd-pix."
                "Kontext databáze byl změněn na jd-pix."
                "Adatbázis-környezet módosítva a következő értékre: „jd-pix”."
                "Zmieniono kontekst bazy danych na „jd-pix”."
                "Το περιβάλλον της βάσης δεδομένων άλλαξε σε 'jd-pix'."
                "Контекст базы данных изменен на \"jd-pix\"."
                "Veritabanı bağlamı 'jd-pix' olarak değiştirildi."
                "Contexto do banco de dados alterado para 'jd-pix'."
                "已將資料庫內容變更為 'jd-pix'。"
                "데이터베이스 컨텍스트가 'jd-pix'(으)로 변경되었습니다."
                "已将数据库上下文更改为 \"jd-pix\"。"
                "Endret databasekontekst til jd-pix."]]
    (is (= "id\n--\n3\n" (mssql-database/strip-output (str line "\nid\n--\n3\n")))
        line)))

(deftest keeps-the-error
  (let [output "Incorrect syntax near '-'.\n\nfailed executing command, reason=exit status 1"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-the-header
  (let [output "id\n--\n3\n\n(1 row affected)\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-an-error-that-arrives-before-the-context-line
  (let [output "Invalid object name 'dbo.nope'.\n\nChanged database context to 'jd-pix'.\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest keeps-a-line-that-only-starts-like-the-context-line
  (let [output "Changed database context to 'jd-pix'. Extra\nid\n"]
    (is (= output (mssql-database/strip-output output)))))

(deftest missing-output-stays-missing
  (is (nil? (mssql-database/strip-output nil))))

(deftest terminal-script-hides-its-prefix
  (is (= user-script (mssql-database/strip-script
                      (mssql-database/with-database "jd-pix" user-script))))
  (is (= user-script (mssql-database/strip-script user-script))))
