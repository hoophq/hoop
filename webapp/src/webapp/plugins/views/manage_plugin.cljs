(ns webapp.plugins.views.manage-plugin
  (:require
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.plugins.views.plugins-configurations :as plugins-configurations]))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])]
    (fn []
      (let [installed? (or (-> @plugin-details :plugin :installed?) false)
            plugin-name (or (-> @plugin-details :plugin :name) "")]
        [:div
         {:class (str "h-full flex flex-col gap-small"
                      " px-large py-regular bg-white")}
         [:header {:class "flex mb-regular"}
          [:div {:class "bg-gray-700 px-3 py-2 text-white rounded-lg"}
           [:h1 {:class "text-2xl"}
            (->> (string/split (str plugin-name) #"_")
                 (map string/capitalize)
                 (string/join " "))]]]
         [plugins-configurations/config (if installed?
                                          plugin-name
                                          (str plugin-name "-not-installed"))]]))))
