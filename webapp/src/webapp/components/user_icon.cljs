(ns webapp.components.user-icon
  (:require [clojure.string :as string]))

(defn initials-black [user-name]
  (let [user-name-split (string/split user-name #" ")
        user-name-initials (str (first (take 1 (first user-name-split)))
                                (first (take 1 (last user-name-split))))]
    [:div
     {:class (str "flex items-center justify-center "
                  "rounded-full overflow-hidden w-8 h-8 "
                  "text-xs font-bold text-white bg-gray-800")}
     [:span {:class "uppercase"}
      user-name-initials]]))

(defn initials-white [user-name]
  (let [user-name-split (string/split user-name #" ")
        user-name-initials (str (first (take 1 (first user-name-split)))
                                (first (take 1 (last user-name-split))))]
    [:div
     {:class (str "flex items-center justify-center "
                  "rounded-full overflow-hidden w-8 h-8 "
                  "text-xs font-bold text-gray-900 bg-white")}
     [:span {:class "uppercase"}
      user-name-initials]]))

(defn email-black [user-email]
  (let [user-email-first-letter (take 1 user-email)]
    [:div
     {:class (str "flex items-center justify-center "
                  "rounded-full overflow-hidden w-8 h-8 "
                  "text-xs font-bold text-white bg-gray-800")}
     [:span {:class "uppercase"}
      user-email-first-letter
      user-email-first-letter]]))
