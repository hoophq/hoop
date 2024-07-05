(ns webapp.components.icon)

(defn- base [{:keys [size icon-name role]}]
  [:figure {:class (str "w-" (or size 5))}
   [:img {:role (or role "icon")
          :src (str "/icons/icon-" icon-name ".svg")}]])

(defn regular [{:keys [size icon-name role]}]
  [base {:size size
         :icon-name icon-name
         :role role}])

(defn hero-icon [{:keys [size icon role]}]
  [:figure {:class (str "p-0.5 w-" (or size 5))}
   [:img {:role (or role "icon")
          :src (str "/icons/hero-icon-" icon ".svg")}]])

