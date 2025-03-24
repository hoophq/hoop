(ns webapp.plugins.views.manage-plugin
  (:require ["@radix-ui/themes" :refer [Box]]
            [clojure.string :as string]
            [re-frame.core :as rf]
            [webapp.components.headings :as h]
            [webapp.plugins.views.plugins-configurations :as plugins-configurations]))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])]
    (fn []
      (let [installed? (or (-> @plugin-details :plugin :installed?) false)
            plugin-name (or (-> @plugin-details :plugin :name) "")]
        [:div {:class "flex flex-col bg-gray-100 px-4 py-10 sm:px-6 lg:px-20 lg:pt-16 lg:pb-10 h-full"}
         [h/h2 (->> (string/split (str plugin-name) #"_")
                    (map string/capitalize)
                    (string/join " ")) {:class "mb-6"}]
         [:> Box {:p "5" :minHeight "800px" :class "bg-white rounded-md border border-gray-100 overflow-y-auto"}
          [plugins-configurations/config (if installed?
                                           plugin-name
                                           (str plugin-name "-not-installed"))]]]))))
